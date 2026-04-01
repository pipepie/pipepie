// Package server implements the pipepie relay server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	cfDNS "github.com/libdns/cloudflare"
	"encoding/hex"
	"os"

	"github.com/flynn/noise"
	"golang.org/x/crypto/acme/autocert"

	"github.com/pipepie/pipepie/internal/protocol"
	"github.com/pipepie/pipepie/internal/protocol/pb"
	"github.com/pipepie/pipepie/internal/store"
)

// PipelineRule maps a webhook path prefix to a pipeline step.
type PipelineRule struct {
	PathPrefix string
	PipelineID string
	StepName   string
}

// Config holds server configuration.
type Config struct {
	Addr            string        // HTTP listen address
	TunnelAddr      string        // TCP listen address for Noise+yamux
	Domain          string        // Base domain for subdomains
	KeyFile         string        // Path to Noise static key file
	DBPath          string        // SQLite path
	MaxBody         int64         // Max webhook body size
	Retention       time.Duration // Request retention
	RequestTTL      time.Duration // Timeout waiting for client response
	AutoTLS         bool          // Enable Let's Encrypt autocert
	TLSCacheDir     string        // Directory for TLS cert cache
	TLSCert         string        // Manual TLS cert file
	TLSKey          string        // Manual TLS key file
	CloudflareToken string        // Cloudflare API token for DNS-01
	PipelineRules   []PipelineRule
	TCPProxies      []TCPProxyConfig
}

// TCPProxyConfig defines a TCP proxy listener.
type TCPProxyConfig struct {
	ListenAddr string
	Subdomain  string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Addr:       ":8080",
		TunnelAddr: ":9443",
		Domain:     "localhost",
		DBPath:     "pipepie.db",
		MaxBody:    10 << 20,
		Retention:  72 * time.Hour,
		RequestTTL: 30 * time.Second,
	}
}

// Server is the pipepie relay server.
type Server struct {
	cfg      Config
	store    *store.Store
	hub      *Hub
	httpSrv  *http.Server
	key      noise.DHKey
	dashAuth *DashboardAuth
	pipeline *PipelineTracker
	log      *slog.Logger
}

// New creates a new server.
func New(cfg Config, log *slog.Logger) (*Server, error) {
	st, err := store.New(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	key, err := loadOrCreateKey(cfg.KeyFile, log)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("noise key: %w", err)
	}
	return &Server{
		cfg:      cfg,
		store:    st,
		hub:      NewHub(log),
		key:      key,
		dashAuth: NewDashboardAuth(),
		pipeline: NewPipelineTracker(60 * time.Second),
		log:      log,
	}, nil
}

// loadOrCreateKey reads or generates the Noise NK server keypair.
func loadOrCreateKey(path string, log *slog.Logger) (noise.DHKey, error) {
	if path == "" {
		path = "pipepie.key"
	}

	// Try to load existing key
	if data, err := os.ReadFile(path); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) >= 2 {
			priv, err1 := hex.DecodeString(strings.TrimSpace(lines[0]))
			pub, err2 := hex.DecodeString(strings.TrimSpace(lines[1]))
			if err1 == nil && err2 == nil && len(priv) == 32 && len(pub) == 32 {
				log.Info("loaded server key", "pubkey", lines[1], "file", path)
				return noise.DHKey{Private: priv, Public: pub}, nil
			}
		}
	}

	// Generate new key
	kp, err := protocol.GenerateKeypair()
	if err != nil {
		return noise.DHKey{}, err
	}
	content := hex.EncodeToString(kp.Private) + "\n" + hex.EncodeToString(kp.Public) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return noise.DHKey{}, fmt.Errorf("save key: %w", err)
	}
	pubHex := hex.EncodeToString(kp.Public)
	log.Info("generated new server key", "pubkey", pubHex, "file", path)
	return kp, nil
}

// Close cleanly shuts down the server and store.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.httpSrv != nil {
		s.httpSrv.Shutdown(ctx)
	}
	return s.store.Close()
}

// Run starts the HTTP server and tunnel listener.
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// API
	mux.HandleFunc("GET /api/tunnels/{subdomain}/requests", s.handleRequestList)
	mux.HandleFunc("GET /api/tunnels/{subdomain}/requests/{id}", s.handleRequestGet)
	mux.HandleFunc("GET /api/requests/{id}", s.handleRequestGetGlobal) // lookup by ID across all tunnels
	mux.HandleFunc("POST /api/tunnels/{subdomain}/requests/{id}/replay", s.handleReplay)
	mux.HandleFunc("GET /api/tunnels/{subdomain}/status", s.handleTunnelStatus)

	// Pipeline tracing
	mux.HandleFunc("GET /api/pipelines/{pipeline_id}/traces", s.handlePipelineTraces)
	mux.HandleFunc("GET /api/traces/{trace_id}", s.handleTraceTimeline)
	mux.HandleFunc("POST /api/pipeline-rules", s.handleSetPipelineRules)

	// Dashboard overview (no admin required)
	mux.HandleFunc("GET /api/overview", s.handleOverview)

	// Admin
	mux.HandleFunc("POST /api/admin/tunnels", s.handleTunnelCreate)
	mux.HandleFunc("GET /api/admin/tunnels", s.handleTunnelList)
	mux.HandleFunc("DELETE /api/admin/tunnels/{subdomain}", s.handleTunnelDelete)

	// Web UI
	mux.HandleFunc("GET /ui/{path...}", s.handleUI)

	// Health
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Rate limiting + dashboard auth
	rl := NewRateLimiter(100, 200)
	handler := rl.Middleware(s.dashAuth.Middleware(s.subdomainRouter(mux)))

	// Background tasks
	go s.retentionLoop()

	// Tunnel listener (Noise + yamux + Protobuf)
	tl := NewTunnelListener(s.cfg.TunnelAddr, s.cfg.Domain, s.key, s.store, s.hub, s.dashAuth, s.log)
	go func() {
		if err := tl.Run(); err != nil {
			s.log.Error("tunnel listener failed", "err", err)
		}
	}()

	// Start TCP proxies if configured
	for _, tp := range s.cfg.TCPProxies {
		proxy := NewTCPProxy(tp.ListenAddr, tp.Subdomain, s.hub, s.log)
		go func() {
			if err := proxy.Run(); err != nil {
				s.log.Error("tcp proxy failed", "err", err, "addr", tp.ListenAddr)
			}
		}()
	}

	s.httpSrv = &http.Server{Addr: s.cfg.Addr, Handler: handler}

	s.log.Info("pipepie server starting",
		"http", s.cfg.Addr,
		"tunnel", s.cfg.TunnelAddr,
		"domain", s.cfg.Domain,
		"tls", s.cfg.AutoTLS,
	)

	// Manual TLS cert
	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		s.httpSrv.Addr = ":443"
		s.log.Info("TLS enabled", "mode", "manual", "cert", s.cfg.TLSCert)
		go http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
		}))
		return s.httpSrv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}

	// Auto TLS (per-subdomain via autocert, or Cloudflare wildcard via certmagic)
	if s.cfg.AutoTLS {
		if s.cfg.CloudflareToken != "" {
			return s.runWithCertmagic()
		}
		return s.runWithAutocert()
	}

	return s.httpSrv.ListenAndServe()
}

func (s *Server) runWithAutocert() error {
	cacheDir := s.cfg.TLSCacheDir
	if cacheDir == "" {
		cacheDir = "pipepie-certs"
	}
	domain := s.cfg.Domain
	m := &autocert.Manager{
		Cache:  autocert.DirCache(cacheDir),
		Prompt: autocert.AcceptTOS,
		HostPolicy: func(_ context.Context, host string) error {
			if host == domain {
				return nil
			}
			if strings.HasSuffix(host, "."+domain) && !strings.Contains(strings.TrimSuffix(host, "."+domain), ".") {
				return nil
			}
			return fmt.Errorf("host %q not allowed", host)
		},
	}
	s.httpSrv.TLSConfig = m.TLSConfig()
	s.httpSrv.Addr = ":443"
	go http.ListenAndServe(":80", m.HTTPHandler(nil))
	s.log.Info("TLS enabled", "mode", "autocert", "domain", domain)
	return s.httpSrv.ListenAndServeTLS("", "")
}

func (s *Server) runWithCertmagic() error {
	cacheDir := s.cfg.TLSCacheDir
	if cacheDir == "" {
		cacheDir = "pipepie-certs"
	}
	domain := s.cfg.Domain

	storage := &certmagic.FileStorage{Path: cacheDir}
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			c := certmagic.Default; return &c, nil
		},
	})
	cfg := certmagic.New(cache, certmagic.Config{Storage: storage})

	issuer := certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
		CA:                      certmagic.LetsEncryptProductionCA,
		Agreed:                  true,
		DisableHTTPChallenge:    true,
		DisableTLSALPNChallenge: true,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &cfDNS.Provider{APIToken: s.cfg.CloudflareToken},
			},
		},
	})
	cfg.Issuers = []certmagic.Issuer{issuer}

	s.log.Info("TLS enabled", "mode", "cloudflare-dns01", "domain", domain)
	if err := cfg.ManageSync(context.Background(), []string{"*."+domain, domain}); err != nil {
		return fmt.Errorf("certmagic: %w", err)
	}

	s.httpSrv.TLSConfig = cfg.TLSConfig()
	s.httpSrv.Addr = ":443"
	go http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
	}))
	return s.httpSrv.ListenAndServeTLS("", "")
}

// ── Subdomain routing ────────────────────────────────────────────────

func (s *Server) subdomainRouter(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := s.extractSubdomain(r.Host)
		if sub == "" {
			fallback.ServeHTTP(w, r)
			return
		}
		// WebSocket upgrade → proxy as raw TCP stream through yamux
		if isWebSocketUpgrade(r) {
			s.handleWebSocketProxy(w, r, sub)
			return
		}
		// SSE/streaming requests → also proxy as raw TCP (prevents buffering)
		if isSSERequest(r) {
			s.handleWebSocketProxy(w, r, sub) // reuses same hijack+yamux path
			return
		}
		s.handleWebhook(w, r, sub)
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func isSSERequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream")
}

func (s *Server) extractSubdomain(host string) string {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	if !strings.HasSuffix(host, "."+s.cfg.Domain) {
		return ""
	}
	sub := strings.TrimSuffix(host, "."+s.cfg.Domain)
	if strings.Contains(sub, ".") {
		return ""
	}
	return sub
}

// ── WebSocket proxy (raw TCP through yamux) ──────────────────────────

func (s *Server) handleWebSocketProxy(w http.ResponseWriter, r *http.Request, subdomain string) {
	sess := s.hub.Get(subdomain)
	if sess == nil {
		http.Error(w, "tunnel not connected", http.StatusBadGateway)
		return
	}

	ns, ok := sess.(*noiseSession)
	if !ok {
		http.Error(w, "tunnel does not support websocket proxy", http.StatusBadGateway)
		return
	}

	// Open yamux stream to client
	stream, err := ns.sess.Open()
	if err != nil {
		http.Error(w, "failed to open stream", http.StatusBadGateway)
		return
	}

	// Send TCP proxy marker (0x01) so client knows it's a raw proxy
	stream.Write([]byte{0x01})

	// Hijack the HTTP connection
	hj, ok := w.(http.Hijacker)
	if !ok {
		stream.Close()
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}

	clientConn, buf, err := hj.Hijack()
	if err != nil {
		stream.Close()
		return
	}

	// Write the original HTTP request to the yamux stream (so the local server sees the upgrade)
	r.Write(stream)

	// Bidirectional proxy
	go func() {
		if buf.Reader.Buffered() > 0 {
			buffered := make([]byte, buf.Reader.Buffered())
			buf.Read(buffered)
			stream.Write(buffered)
		}
		io.Copy(stream, clientConn)
		stream.Close()
	}()
	io.Copy(clientConn, stream)
	clientConn.Close()
}

// ── Webhook proxy ────────────────────────────────────────────────────

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request, subdomain string) {
	tunnel, err := s.store.TunnelBySubdomain(subdomain)
	if err != nil {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	headersJSON, _ := json.Marshal(flattenHeaders(r.Header))

	// Pipeline tracing: from headers or auto-matched rules or auto-detected provider
	traceID := r.Header.Get("X-Pipepie-Trace-ID")
	pipelineID := r.Header.Get("X-Pipepie-Pipeline")
	stepName := r.Header.Get("X-Pipepie-Step")

	// 1. Auto-match pipeline rules by path prefix
	if pipelineID == "" {
		for _, rule := range s.cfg.PipelineRules {
			if strings.HasPrefix(r.URL.Path, rule.PathPrefix) {
				pipelineID = rule.PipelineID
				stepName = rule.StepName
				break
			}
		}
	}

	// 2. Auto-detect AI provider from payload (Replicate, fal.ai, RunPod, etc.)
	if pipelineID == "" {
		if match := DetectProvider(r.Header, body, r.URL.Path); match != nil {
			pipelineID = match.Provider
			stepName = match.StepName
			if match.Model != "" {
				stepName = match.StepName + ":" + match.Model
			}
			// Use job ID as trace correlation — same job = same trace
			if match.JobID != "" && traceID == "" {
				traceID = match.Provider + ":" + match.JobID
			}
			s.log.Debug("auto-detected provider",
				"provider", match.Provider,
				"job", match.JobID,
				"status", match.Status,
			)
		}
	}

	// 3. Auto-correlate: group pipeline requests within time window
	if pipelineID != "" && traceID == "" {
		traceID = s.pipeline.Correlate(tunnel.ID, pipelineID)
	}

	// 4. Auto step name from path if still not set
	if stepName == "" && pipelineID != "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) > 0 {
			stepName = parts[len(parts)-1]
		}
	}

	var insertOpts *store.RequestInsertOpts
	if traceID != "" || pipelineID != "" {
		insertOpts = &store.RequestInsertOpts{
			PipelineID: pipelineID,
			StepName:   stepName,
			TraceID:    traceID,
		}
	}

	reqID, err := s.store.RequestInsert(
		tunnel.ID, r.Method, r.URL.Path, r.URL.RawQuery,
		string(headersJSON), body, remoteIP(r), insertOpts,
	)
	if err != nil {
		s.log.Error("store request failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Compress body for wire
	compBody, compressed := protocol.CompressBody(body)

	pbReq := &pb.HttpRequest{
		Id:         reqID,
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.RawQuery,
		Headers:    flattenHeaders(r.Header),
		Body:       compBody,
		Compressed: compressed,
	}

	resp, _ := s.hub.SendRequest(subdomain, pbReq, s.cfg.RequestTTL)
	if resp == nil {
		s.store.RequestSetTimeout(reqID)
		http.Error(w, "tunnel client not connected or timeout", http.StatusGatewayTimeout)
		return
	}

	// Decompress response
	respBody, _ := protocol.DecompressBody(resp.Body, resp.Compressed)
	respHeadersJSON, _ := json.Marshal(resp.Headers)
	s.store.RequestSetResponse(reqID, int(resp.Status), string(respHeadersJSON), respBody, resp.DurationMs)

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(int(resp.Status))
	w.Write(respBody)
}

// ── Retention cleanup ────────────────────────────────────────────────

func (s *Server) retentionLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := s.store.RequestPrune(s.cfg.Retention)
		if err != nil {
			s.log.Error("prune failed", "err", err)
		} else if n > 0 {
			s.log.Info("pruned old requests", "count", n)
		}
	}
}
