package server

import (
	"log/slog"
	"sync"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
)

// TunnelSession is the unified interface for any connected tunnel client.
type TunnelSession interface {
	SendRequest(req *pb.HttpRequest, timeout time.Duration) (*pb.HttpResponse, error)
	Subdomain() string
	Protocol() string
	RemoteAddr() string
	ConnectedAt() time.Time
	Close() error
}

// Hub manages active tunnel sessions keyed by subdomain.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]TunnelSession
	log      *slog.Logger
}

// NewHub creates a new connection hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		sessions: make(map[string]TunnelSession),
		log:      log,
	}
}

// Register adds a session, closing any existing one on the same subdomain.
func (h *Hub) Register(s TunnelSession) {
	var toClose TunnelSession
	h.mu.Lock()
	if old, ok := h.sessions[s.Subdomain()]; ok {
		toClose = old
	}
	h.sessions[s.Subdomain()] = s
	h.mu.Unlock()

	// Close outside the lock to avoid blocking
	if toClose != nil {
		toClose.Close()
		h.log.Info("replaced existing session", "subdomain", s.Subdomain())
	}
	h.log.Info("session registered", "subdomain", s.Subdomain(), "protocol", s.Protocol())
}

// Unregister removes a session (only if it matches the given instance).
func (h *Hub) Unregister(s TunnelSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.sessions[s.Subdomain()]; ok && cur == s {
		delete(h.sessions, s.Subdomain())
		h.log.Info("session unregistered", "subdomain", s.Subdomain())
	}
}

// Get returns the session for a subdomain, or nil.
func (h *Hub) Get(subdomain string) TunnelSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[subdomain]
}

// IsOnline checks if a subdomain has a connected session.
func (h *Hub) IsOnline(subdomain string) bool {
	return h.Get(subdomain) != nil
}

// SendRequest forwards a request to the appropriate session.
func (h *Hub) SendRequest(subdomain string, req *pb.HttpRequest, timeout time.Duration) (*pb.HttpResponse, error) {
	s := h.Get(subdomain)
	if s == nil {
		return nil, nil
	}
	return s.SendRequest(req, timeout)
}
