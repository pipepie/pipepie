package integration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pipepie/pipepie/internal/client"
	"github.com/pipepie/pipepie/internal/protocol"
	"github.com/pipepie/pipepie/internal/server"
)

type testEnv struct {
	serverAddr string
	tunnelAddr string
	pubKey     []byte // server's Noise public key
	cancel     context.CancelFunc
}

func setupEnv(t *testing.T) *testEnv {
	t.Helper()

	httpPort := freePort(t)
	tunnelPort := freePort(t)

	// Generate server key
	kp, err := protocol.GenerateKeypair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "test.key")
	os.WriteFile(keyFile, []byte(hex.EncodeToString(kp.Private)+"\n"+hex.EncodeToString(kp.Public)+"\n"), 0600)

	cfg := server.Config{
		Addr:       fmt.Sprintf(":%d", httpPort),
		TunnelAddr: fmt.Sprintf(":%d", tunnelPort),
		Domain:     "localhost",
		KeyFile:    keyFile,
		DBPath:     ":memory:",
		MaxBody:    10 << 20,
		Retention:  1 * time.Hour,
		RequestTTL: 5 * time.Second,
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := server.New(cfg, log)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run()
	time.Sleep(300 * time.Millisecond)

	env := &testEnv{
		serverAddr: fmt.Sprintf("localhost:%d", httpPort),
		tunnelAddr: fmt.Sprintf("localhost:%d", tunnelPort),
		pubKey:     kp.Public,
		cancel:     cancel,
	}
	t.Cleanup(func() { cancel(); _ = ctx })
	return env
}

func (e *testEnv) connectClient(t *testing.T, subdomain, forward string) context.CancelFunc {
	t.Helper()
	cfg := client.Config{
		ServerAddr:   e.tunnelAddr,
		ServerPubKey: e.pubKey,
		Subdomain:    subdomain,
		Forward:      forward,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go client.New(cfg).Run(ctx)
	time.Sleep(500 * time.Millisecond)
	return cancel
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func localApp(t *testing.T) (string, int) {
	t.Helper()
	port := freePort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"path":   r.URL.Path,
			"method": r.Method,
			"body":   string(body),
		})
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close() })
	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://localhost:%d", port), port
}

// ── Tests ────────────────────────────────────────────────────────────

func TestE2E_SimpleWebhook(t *testing.T) {
	env := setupEnv(t)
	appURL, _ := localApp(t)

	stop := env.connectClient(t, "test", appURL)
	defer stop()

	resp, err := http.Post(
		"http://test."+env.serverAddr+"/stripe/webhook",
		"application/json",
		strings.NewReader(`{"event":"payment.succeeded"}`),
	)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["path"] != "/stripe/webhook" {
		t.Errorf("path = %v", result["path"])
	}
}

func TestE2E_AnonymousTunnel(t *testing.T) {
	env := setupEnv(t)
	appURL, _ := localApp(t)

	// Connect without subdomain — gets auto-assigned
	cfg := client.Config{
		ServerAddr:   env.tunnelAddr,
		ServerPubKey: env.pubKey,
		Forward:      appURL,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.New(cfg).Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Find the auto-assigned subdomain via overview API
	resp, err := http.Get("http://" + env.serverAddr + "/api/overview")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	defer resp.Body.Close()

	var data struct {
		Tunnels []struct {
			Subdomain string `json:"subdomain"`
			Online    bool   `json:"online"`
		} `json:"tunnels"`
	}
	json.NewDecoder(resp.Body).Decode(&data)

	found := false
	for _, tun := range data.Tunnels {
		if tun.Online {
			r, err := http.Post("http://"+tun.Subdomain+"."+env.serverAddr+"/test", "text/plain", strings.NewReader("hi"))
			if err == nil && r.StatusCode == 200 {
				r.Body.Close()
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("could not reach ephemeral tunnel")
	}
}

func TestE2E_NoClient_Returns504(t *testing.T) {
	env := setupEnv(t)

	// Create tunnel via API but don't connect a client
	http.Post("http://"+env.serverAddr+"/api/admin/tunnels", "application/json",
		strings.NewReader(`{"subdomain":"offline"}`))

	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Post("http://offline."+env.serverAddr+"/test", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 504 {
		t.Errorf("status = %d, want 504", resp.StatusCode)
	}
}

func TestE2E_LargeBody_Compressed(t *testing.T) {
	env := setupEnv(t)
	appURL, _ := localApp(t)

	stop := env.connectClient(t, "large", appURL)
	defer stop()

	bigBody := strings.Repeat("X", 50000)
	resp, err := http.Post("http://large."+env.serverAddr+"/big", "text/plain", strings.NewReader(bigBody))
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result["body"].(string)) != 50000 {
		t.Errorf("body length = %d", len(result["body"].(string)))
	}
}

func TestE2E_RequestList_API(t *testing.T) {
	env := setupEnv(t)
	appURL, _ := localApp(t)

	stop := env.connectClient(t, "api", appURL)
	defer stop()

	for i := range 3 {
		http.Post(fmt.Sprintf("http://api.%s/hook/%d", env.serverAddr, i), "text/plain", strings.NewReader("x"))
	}
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get("http://" + env.serverAddr + "/api/tunnels/api/requests?limit=10")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()

	var data struct {
		Total int `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&data)
	if data.Total != 3 {
		t.Errorf("total = %d, want 3", data.Total)
	}
}

func TestE2E_TunnelStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping disconnect detection test in short mode")
	}
	env := setupEnv(t)
	appURL, _ := localApp(t)

	// Before connect
	resp, _ := http.Get("http://" + env.serverAddr + "/api/tunnels/status/status")
	var before map[string]any
	json.NewDecoder(resp.Body).Decode(&before)
	resp.Body.Close()

	stop := env.connectClient(t, "status", appURL)

	resp, _ = http.Get("http://" + env.serverAddr + "/api/tunnels/status/status")
	var after map[string]any
	json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if after["online"] != true {
		t.Error("should be online after connect")
	}

	stop()
	var offline bool
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		resp, _ = http.Get("http://" + env.serverAddr + "/api/tunnels/status/status")
		var d map[string]any
		json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if d["online"] == false {
			offline = true
			break
		}
	}
	if !offline {
		t.Error("should be offline after disconnect")
	}
}

func TestE2E_Health(t *testing.T) {
	env := setupEnv(t)
	resp, err := http.Get("http://" + env.serverAddr + "/healthz")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
