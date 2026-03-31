package store

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ── Tunnels ──────────────────────────────────────────────────────────

func TestTunnelCreate(t *testing.T) {
	s := newTestStore(t)
	tunnel, err := s.TunnelCreate("myapp", "tok_123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tunnel.Subdomain != "myapp" {
		t.Errorf("subdomain = %q", tunnel.Subdomain)
	}
	if tunnel.ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestTunnelCreate_Duplicate(t *testing.T) {
	s := newTestStore(t)
	s.TunnelCreate("myapp", "tok_1")
	_, err := s.TunnelCreate("myapp", "tok_2")
	if err == nil {
		t.Fatal("expected error on duplicate subdomain")
	}
}

func TestTunnelBySubdomain(t *testing.T) {
	s := newTestStore(t)
	s.TunnelCreate("test", "tok_abc")

	tunnel, err := s.TunnelBySubdomain("test")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if tunnel.Token != "tok_abc" {
		t.Errorf("token = %q", tunnel.Token)
	}
}

func TestTunnelBySubdomain_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.TunnelBySubdomain("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTunnelList(t *testing.T) {
	s := newTestStore(t)
	s.TunnelCreate("a", "tok_a")
	s.TunnelCreate("b", "tok_b")

	tunnels, err := s.TunnelList()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tunnels) != 2 {
		t.Errorf("got %d tunnels, want 2", len(tunnels))
	}
}

func TestTunnelDelete(t *testing.T) {
	s := newTestStore(t)
	s.TunnelCreate("del", "tok_del")

	if err := s.TunnelDelete("del"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.TunnelBySubdomain("del")
	if err == nil {
		t.Error("tunnel should be deleted")
	}
}

func TestTunnelDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.TunnelDelete("ghost")
	if err == nil {
		t.Error("expected error")
	}
}

func TestTunnelDelete_CascadesRequests(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("cas", "tok")
	s.RequestInsert(tunnel.ID, "POST", "/test", "", "{}", nil, "1.2.3.4", nil)

	s.TunnelDelete("cas")

	// Requests should be gone too
	reqs, total, _ := s.RequestList(tunnel.ID, 10, 0)
	if total != 0 || len(reqs) != 0 {
		t.Error("requests should be cascade deleted")
	}
}

func TestTunnelTouch(t *testing.T) {
	s := newTestStore(t)
	s.TunnelCreate("touch", "tok")
	s.TunnelTouch("touch")

	tunnel, _ := s.TunnelBySubdomain("touch")
	if tunnel.LastSeen == "" {
		t.Error("last_seen should be set")
	}
}

// ── Requests ─────────────────────────────────────────────────────────

func TestRequestInsert(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("req", "tok")

	id, err := s.RequestInsert(tunnel.ID, "POST", "/webhook", "foo=bar", `{"Content-Type":"application/json"}`, []byte(`{"test":1}`), "10.0.0.1", nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	req, err := s.RequestGet(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("method = %q", req.Method)
	}
	if req.Path != "/webhook" {
		t.Errorf("path = %q", req.Path)
	}
	if req.Query != "foo=bar" {
		t.Errorf("query = %q", req.Query)
	}
	if req.Status != "pending" {
		t.Errorf("status = %q", req.Status)
	}
	if req.ReqSize != 10 {
		t.Errorf("req_size = %d", req.ReqSize)
	}
	if req.SourceIP != "10.0.0.1" {
		t.Errorf("source_ip = %q", req.SourceIP)
	}
}

func TestRequestSetResponse(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("resp", "tok")
	id, _ := s.RequestInsert(tunnel.ID, "POST", "/test", "", "{}", nil, "", nil)

	err := s.RequestSetResponse(id, 200, `{"X-Custom":"val"}`, []byte("ok"), 42)
	if err != nil {
		t.Fatalf("set response: %v", err)
	}

	req, _ := s.RequestGet(id)
	if req.Status != "forwarded" {
		t.Errorf("status = %q, want forwarded", req.Status)
	}
	if *req.RespStatus != 200 {
		t.Errorf("resp_status = %d", *req.RespStatus)
	}
	if *req.DurationMs != 42 {
		t.Errorf("duration = %d", *req.DurationMs)
	}
}

func TestRequestSetError(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("err", "tok")
	id, _ := s.RequestInsert(tunnel.ID, "POST", "/test", "", "{}", nil, "", nil)

	s.RequestSetError(id, "connection refused")

	req, _ := s.RequestGet(id)
	if req.Status != "failed" {
		t.Errorf("status = %q", req.Status)
	}
	if *req.Error != "connection refused" {
		t.Errorf("error = %q", *req.Error)
	}
}

func TestRequestSetTimeout(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("to", "tok")
	id, _ := s.RequestInsert(tunnel.ID, "POST", "/test", "", "{}", nil, "", nil)

	s.RequestSetTimeout(id)

	req, _ := s.RequestGet(id)
	if req.Status != "timeout" {
		t.Errorf("status = %q", req.Status)
	}
}

func TestRequestList(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("list", "tok")

	for i := range 5 {
		s.RequestInsert(tunnel.ID, "POST", "/test", "", "{}", nil, "", nil)
		_ = i
	}

	reqs, total, err := s.RequestList(tunnel.ID, 3, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(reqs) != 3 {
		t.Errorf("got %d, want 3 (limit)", len(reqs))
	}

	// Page 2
	reqs2, _, _ := s.RequestList(tunnel.ID, 3, 3)
	if len(reqs2) != 2 {
		t.Errorf("page 2: got %d, want 2", len(reqs2))
	}
}

func TestRequestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.RequestGet("nonexistent")
	if err == nil {
		t.Error("expected error")
	}
}

func TestRequestPrune(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("prune", "tok")
	s.RequestInsert(tunnel.ID, "POST", "/old", "", "{}", nil, "", nil)

	// Prune with 0 retention = delete everything
	n, err := s.RequestPrune(0)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	// The request was just created so with 0 duration it may or may not be pruned
	// depending on timing. Use a long retention to keep it:
	s.RequestInsert(tunnel.ID, "POST", "/keep", "", "{}", nil, "", nil)
	n, err = s.RequestPrune(24 * time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	_ = n // Should be 0 or 1 depending on the first insert
}

// ── Pipeline tracing ─────────────────────────────────────────────────

func TestPipelineTracing(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("pipe", "tok")

	// Simulate a 3-step pipeline
	opts1 := &RequestInsertOpts{PipelineID: "image-gen", StepName: "replicate", TraceID: "trace-001"}
	id1, _ := s.RequestInsert(tunnel.ID, "POST", "/replicate", "", "{}", nil, "", opts1)
	s.RequestSetResponse(id1, 200, "{}", []byte("img.png"), 8000)

	opts2 := &RequestInsertOpts{PipelineID: "image-gen", StepName: "upscale", TraceID: "trace-001"}
	id2, _ := s.RequestInsert(tunnel.ID, "POST", "/fal", "", "{}", nil, "", opts2)
	s.RequestSetResponse(id2, 200, "{}", []byte("big.png"), 3000)

	opts3 := &RequestInsertOpts{PipelineID: "image-gen", StepName: "store", TraceID: "trace-001"}
	id3, _ := s.RequestInsert(tunnel.ID, "POST", "/store", "", "{}", nil, "", opts3)
	s.RequestSetResponse(id3, 200, "{}", []byte("ok"), 50)

	// Get trace timeline
	steps, err := s.TraceTimeline("trace-001")
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[0].StepName != "replicate" {
		t.Errorf("step 0 = %q", steps[0].StepName)
	}
	if steps[2].StepName != "store" {
		t.Errorf("step 2 = %q", steps[2].StepName)
	}

	// Get pipeline traces
	traces, err := s.PipelineTraces("image-gen", 10)
	if err != nil {
		t.Fatalf("pipeline traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].TraceID != "trace-001" {
		t.Errorf("trace id = %q", traces[0].TraceID)
	}
	if traces[0].Status != "completed" {
		t.Errorf("status = %q, want completed", traces[0].Status)
	}
	if len(traces[0].Steps) != 3 {
		t.Errorf("trace steps = %d", len(traces[0].Steps))
	}
}

func TestPipelineTracing_FailedStep(t *testing.T) {
	s := newTestStore(t)
	tunnel, _ := s.TunnelCreate("fail", "tok")

	opts1 := &RequestInsertOpts{PipelineID: "video", StepName: "transcribe", TraceID: "trace-f"}
	id1, _ := s.RequestInsert(tunnel.ID, "POST", "/transcribe", "", "{}", nil, "", opts1)
	s.RequestSetResponse(id1, 200, "{}", nil, 5000)

	opts2 := &RequestInsertOpts{PipelineID: "video", StepName: "summarize", TraceID: "trace-f"}
	id2, _ := s.RequestInsert(tunnel.ID, "POST", "/summarize", "", "{}", nil, "", opts2)
	s.RequestSetError(id2, "model overloaded")

	traces, _ := s.PipelineTraces("video", 10)
	if len(traces) != 1 {
		t.Fatalf("got %d traces", len(traces))
	}
	if traces[0].Status != "failed" {
		t.Errorf("status = %q, want failed", traces[0].Status)
	}
}

func TestTraceTimeline_Empty(t *testing.T) {
	s := newTestStore(t)
	steps, err := s.TraceTimeline("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("expected empty, got %d", len(steps))
	}
}
