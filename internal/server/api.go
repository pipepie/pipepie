package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/protocol"
	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
	"github.com/Seinarukiro2/pipepie/internal/ui"
	"github.com/google/uuid"
)

var uiFS, _ = fs.Sub(ui.Static, ".")


// ── Tunnel admin ─────────────────────────────────────────────────────

func (s *Server) handleTunnelCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subdomain string `json:"subdomain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Subdomain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subdomain is required"})
		return
	}
	if !isValidSubdomain(req.Subdomain) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subdomain"})
		return
	}
	tunnel, err := s.store.TunnelCreate(req.Subdomain, "")
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "subdomain already exists"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        tunnel.ID,
		"subdomain": tunnel.Subdomain,
		"url":       "https://" + tunnel.Subdomain + "." + s.cfg.Domain,
	})
}

func (s *Server) handleTunnelList(w http.ResponseWriter, r *http.Request) {
	tunnels, err := s.store.TunnelList()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type view struct {
		ID        string `json:"id"`
		Subdomain string `json:"subdomain"`
		Online    bool   `json:"online"`
		Protocol  string `json:"protocol,omitempty"`
		CreatedAt string `json:"created_at"`
		LastSeen  string `json:"last_seen,omitempty"`
	}
	out := make([]view, len(tunnels))
	for i, t := range tunnels {
		v := view{
			ID:        t.ID,
			Subdomain: t.Subdomain,
			Online:    s.hub.IsOnline(t.Subdomain),
			CreatedAt: t.CreatedAt,
			LastSeen:  t.LastSeen,
		}
		if sess := s.hub.Get(t.Subdomain); sess != nil {
			v.Protocol = sess.Protocol()
		}
		out[i] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTunnelDelete(w http.ResponseWriter, r *http.Request) {
	subdomain := r.PathValue("subdomain")
	if err := s.store.TunnelDelete(subdomain); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	subdomain := r.PathValue("subdomain")
	proto := ""
	if sess := s.hub.Get(subdomain); sess != nil {
		proto = sess.Protocol()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subdomain": subdomain,
		"online":    s.hub.IsOnline(subdomain),
		"protocol":  proto,
	})
}

// ── Request API ──────────────────────────────────────────────────────

func (s *Server) handleRequestList(w http.ResponseWriter, r *http.Request) {
	subdomain := r.PathValue("subdomain")
	tunnel, err := s.store.TunnelBySubdomain(subdomain)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	reqs, total, err := s.store.RequestList(tunnel.ID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requests": reqs,
		"total":    total,
		"online":   s.hub.IsOnline(subdomain),
	})
}

func (s *Server) handleRequestGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := s.store.RequestGet(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	subdomain := r.PathValue("subdomain")
	id := r.PathValue("id")

	orig, err := s.store.RequestGet(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
		return
	}

	if !s.hub.IsOnline(subdomain) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "client not connected"})
		return
	}

	compBody, compressed := protocol.CompressBody(orig.ReqBody)
	replayReq := &pb.HttpRequest{
		Id:         uuid.NewString(),
		Method:     orig.Method,
		Path:       orig.Path,
		Query:      orig.Query,
		Headers:    mustParseHeaders(orig.ReqHeaders),
		Body:       compBody,
		Compressed: compressed,
		ReplayOf:   orig.ID,
	}

	resp, _ := s.hub.SendRequest(subdomain, replayReq, s.cfg.RequestTTL)
	if resp == nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timeout"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"replay_id": replayReq.Id,
		"status":    resp.Status,
		"duration":  resp.DurationMs,
	})
}

// ── Overview (dashboard landing) ─────────────────────────────────────

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	tunnels, err := s.store.TunnelList()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stats, _ := s.store.TunnelRequestStats()
	lastReqs, _ := s.store.TunnelLastRequest()

	type tunnelOverview struct {
		ID          string `json:"id"`
		Subdomain   string `json:"subdomain"`
		Online      bool   `json:"online"`
		Protocol    string `json:"protocol,omitempty"`
		RemoteAddr  string `json:"remote_addr,omitempty"`
		ConnectedAt string `json:"connected_at,omitempty"`
		Uptime      string `json:"uptime,omitempty"`
		Total       int    `json:"total"`
		Success     int    `json:"success"`
		Errors      int    `json:"errors"`
		SuccessRate string `json:"success_rate"`
		LastRequest string `json:"last_request,omitempty"`
		PublicURL   string `json:"public_url"`
		CreatedAt   string `json:"created_at"`
	}

	out := make([]tunnelOverview, 0, len(tunnels))
	totalReqs := 0
	totalErrors := 0
	onlineCount := 0
	for _, t := range tunnels {
		st := stats[t.ID]
		rate := "—"
		if st.Total > 0 {
			rate = strconv.Itoa(st.Success*100/st.Total) + "%"
		}
		ov := tunnelOverview{
			ID:          t.ID,
			Subdomain:   t.Subdomain,
			Online:      s.hub.IsOnline(t.Subdomain),
			Total:       st.Total,
			Success:     st.Success,
			Errors:      st.Errors,
			SuccessRate: rate,
			LastRequest: lastReqs[t.ID],
			PublicURL:   "https://" + t.Subdomain + "." + s.cfg.Domain,
			CreatedAt:   t.CreatedAt,
		}
		if sess := s.hub.Get(t.Subdomain); sess != nil {
			ov.Protocol = sess.Protocol()
			ov.RemoteAddr = sess.RemoteAddr()
			ov.ConnectedAt = sess.ConnectedAt().Format("2006-01-02T15:04:05Z")
			ov.Uptime = formatUptime(time.Since(sess.ConnectedAt()))
		}
		totalReqs += st.Total
		totalErrors += st.Errors
		if ov.Online {
			onlineCount++
		}
		out = append(out, ov)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tunnels":        out,
		"total_tunnels":  len(tunnels),
		"online":         onlineCount,
		"total_requests": totalReqs,
		"total_errors":   totalErrors,
		"domain":         s.cfg.Domain,
	})
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	if d < 24*time.Hour {
		return strconv.Itoa(int(d.Hours())) + "h " + strconv.Itoa(int(d.Minutes())%60) + "m"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

// ── Pipeline tracing ─────────────────────────────────────────────────

func (s *Server) handlePipelineTraces(w http.ResponseWriter, r *http.Request) {
	pipelineID := r.PathValue("pipeline_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	traces, err := s.store.PipelineTraces(pipelineID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pipeline_id": pipelineID,
		"traces":      traces,
	})
}

func (s *Server) handleTraceTimeline(w http.ResponseWriter, r *http.Request) {
	traceID := r.PathValue("trace_id")
	steps, err := s.store.TraceTimeline(traceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(steps) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trace_id": traceID,
		"steps":    steps,
	})
}

// ── UI ───────────────────────────────────────────────────────────────

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(uiFS, "static/index.html")
	if err != nil {
		http.Error(w, "ui not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// ── helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func isValidSubdomain(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i > 0 && i < len(s)-1 {
			continue
		}
		return false
	}
	return true
}

func mustParseHeaders(raw string) map[string]string {
	m := make(map[string]string)
	json.Unmarshal([]byte(raw), &m)
	return m
}
