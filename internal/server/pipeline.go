package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// PipelineTracker auto-correlates webhook requests into pipeline traces.
// When a request hits a known pipeline path, it assigns a trace_id.
// Subsequent pipeline requests within the correlation window get the same trace_id.
type PipelineTracker struct {
	mu            sync.Mutex
	activeTraces  map[string]*activeTrace // tunnelID → current trace
	correlationTTL time.Duration
}

type activeTrace struct {
	traceID    string
	pipelineID string
	lastSeen   time.Time
}

// NewPipelineTracker creates a tracker with the given correlation window.
func NewPipelineTracker(ttl time.Duration) *PipelineTracker {
	pt := &PipelineTracker{
		activeTraces:  make(map[string]*activeTrace),
		correlationTTL: ttl,
	}
	go pt.cleanup()
	return pt
}

// Correlate returns a trace_id for a pipeline request.
// If there's an active trace for this tunnel within the TTL, returns the same trace_id.
// Otherwise creates a new trace.
func (pt *PipelineTracker) Correlate(tunnelID, pipelineID string) string {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()
	key := tunnelID + ":" + pipelineID

	if at, ok := pt.activeTraces[key]; ok && now.Sub(at.lastSeen) < pt.correlationTTL {
		at.lastSeen = now
		return at.traceID
	}

	// New trace
	traceID := generateTraceID()
	pt.activeTraces[key] = &activeTrace{
		traceID:    traceID,
		pipelineID: pipelineID,
		lastSeen:   now,
	}
	return traceID
}

func generateTraceID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "tr-" + hex.EncodeToString(b)
}

func (pt *PipelineTracker) cleanup() {
	for {
		time.Sleep(time.Minute)
		pt.mu.Lock()
		now := time.Now()
		for k, at := range pt.activeTraces {
			if now.Sub(at.lastSeen) > pt.correlationTTL*2 {
				delete(pt.activeTraces, k)
			}
		}
		pt.mu.Unlock()
	}
}
