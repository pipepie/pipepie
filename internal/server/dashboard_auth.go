package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DashboardAuth manages short-lived tokens and session cookies
// for browser-based dashboard access.
type DashboardAuth struct {
	mu       sync.Mutex
	tokens   map[string]time.Time // token → expiry
	sessions map[string]time.Time // session cookie → expiry
}

func NewDashboardAuth() *DashboardAuth {
	da := &DashboardAuth{
		tokens:   make(map[string]time.Time),
		sessions: make(map[string]time.Time),
	}
	go da.cleanup()
	return da
}

// GenerateToken creates a one-time token valid for 5 minutes.
func (da *DashboardAuth) GenerateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	da.mu.Lock()
	da.tokens[token] = time.Now().Add(5 * time.Minute)
	da.mu.Unlock()

	return token
}

// RedeemToken validates and consumes a one-time token.
// Returns a session ID if valid.
func (da *DashboardAuth) RedeemToken(token string) (string, bool) {
	da.mu.Lock()
	defer da.mu.Unlock()

	exp, ok := da.tokens[token]
	if !ok || time.Now().After(exp) {
		delete(da.tokens, token)
		return "", false
	}
	delete(da.tokens, token) // one-time use

	// Create session
	b := make([]byte, 32)
	rand.Read(b)
	sessionID := hex.EncodeToString(b)
	da.sessions[sessionID] = time.Now().Add(24 * time.Hour)
	return sessionID, true
}

// ValidSession checks if a session cookie is valid.
func (da *DashboardAuth) ValidSession(sessionID string) bool {
	da.mu.Lock()
	defer da.mu.Unlock()
	exp, ok := da.sessions[sessionID]
	return ok && time.Now().Before(exp)
}

// Middleware protects HTTP routes with cookie auth.
// Allows: /healthz, token redemption via ?t= query param.
func (da *DashboardAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Always allow health check and API (API is for internal use)
		if path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		// Only protect /ui/ routes
		if len(path) < 4 || path[:4] != "/ui/" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip auth for localhost (self-hosted dev)
		host := r.Host
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
			next.ServeHTTP(w, r)
			return
		}

		// Check for token in query param (from 'pie dashboard')
		if token := r.URL.Query().Get("t"); token != "" {
			sessionID, ok := da.RedeemToken(token)
			if !ok {
				http.Error(w, "invalid or expired token — run 'pie dashboard' again", http.StatusUnauthorized)
				return
			}
			// Set session cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "pipepie_session",
				Value:    sessionID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400, // 24 hours
			})
			// Redirect to clean URL (remove ?t=)
			cleanURL := *r.URL
			q := cleanURL.Query()
			q.Del("t")
			cleanURL.RawQuery = q.Encode()
			http.Redirect(w, r, cleanURL.String(), http.StatusFound)
			return
		}

		// Check session cookie
		cookie, err := r.Cookie("pipepie_session")
		if err != nil || !da.ValidSession(cookie.Value) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`<!DOCTYPE html>
<html><head><title>pipepie</title></head>
<body style="font-family:system-ui;max-width:500px;margin:80px auto;text-align:center;color:#e1e7ef;background:#0f172a">
<h2>Dashboard access required</h2>
<p style="color:#6b7280">Run <code style="background:#1e293b;padding:2px 8px;border-radius:4px">pie dashboard</code> from your terminal to open the dashboard.</p>
</body></html>`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (da *DashboardAuth) cleanup() {
	for {
		time.Sleep(10 * time.Minute)
		da.mu.Lock()
		now := time.Now()
		for k, exp := range da.tokens {
			if now.After(exp) {
				delete(da.tokens, k)
			}
		}
		for k, exp := range da.sessions {
			if now.After(exp) {
				delete(da.sessions, k)
			}
		}
		da.mu.Unlock()
	}
}
