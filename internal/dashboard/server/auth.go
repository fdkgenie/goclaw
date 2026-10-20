// Package server implements the dashboard's HTTP API server: password auth,
// CSRF Origin checks, rate limiting, and API routing. It never touches the
// database directly; all gateway data comes via the goclaw clients.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
)

// configCtxKey is the context key for the dashboard config.
type configCtxKey struct{}

// ConfigFromCtx returns the dashboard config stored in ctx by middleware.
func ConfigFromCtx(ctx context.Context) *config.Config {
	if c, ok := ctx.Value(configCtxKey{}).(*config.Config); ok {
		return c
	}
	return nil
}

// WithConfig stores the dashboard config in ctx.
func WithConfig(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, configCtxKey{}, cfg)
}

// session holds an authenticated dashboard session.
type session struct {
	created time.Time
}

// SessionManager tracks in-memory dashboard sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*session
	maxAge   time.Duration
}

// NewSessionManager creates a session manager.
func NewSessionManager(maxAge time.Duration) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*session),
		maxAge:   maxAge,
	}
}

// Create issues a new session token.
func (m *SessionManager) Create() string {
	token := randomToken()
	m.mu.Lock()
	m.sessions[token] = &session{created: time.Now()}
	m.mu.Unlock()
	return token
}

// Validate returns true if token is valid and not expired.
func (m *SessionManager) Validate(token string) bool {
	m.mu.RLock()
	s, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Since(s.created) > m.maxAge {
		m.mu.Lock()
		delete(m.sessions, token)
		m.mu.Unlock()
		return false
	}
	return true
}

// Delete removes a session.
func (m *SessionManager) Delete(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// randomToken generates a URL-safe random session token.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.URLEncoding.EncodeToString(b)
}

// RateLimiter tracks per-IP rate limits for login attempts.
type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
}

// NewRateLimiter creates a rate limiter.
func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	return &RateLimiter{limiters: make(map[string]*rate.Limiter), rate: r, burst: burst}
}

// Allow reports whether an event may happen now for the given IP.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.limiters[ip]
	if !ok {
		l = rate.NewLimiter(rl.rate, rl.burst)
		rl.limiters[ip] = l
	}
	return l.Allow()
}
