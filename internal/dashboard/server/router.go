// Package server wires the dashboard HTTP API: password auth, CSRF Origin
// checks, rate limiting, and API routing. Data is fetched via the goclaw
// clients (read-only, allow-listed); the server never touches the database.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/aggregate"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/webui"
)

type Router struct {
	mux   *http.ServeMux
	cfg   *config.Config
	state *live.State
	ws    *goclaw.Client
	http  *goclaw.HTTPClient
	hub   *live.Hub
	sm    *SessionManager
	rl    *RateLimiter
	ring  *activity.RingBuffer
	web   http.Handler
}

// NewRouter builds the API router.
func NewRouter(cfg *config.Config, state *live.State, ws *goclaw.Client, httpClient *goclaw.HTTPClient, hub *live.Hub, sm *SessionManager, rl *RateLimiter, ring *activity.RingBuffer) *Router {
	r := &Router{
		mux:   http.NewServeMux(),
		cfg:   cfg,
		state: state,
		ws:    ws,
		http:  httpClient,
		hub:   hub,
		sm:    sm,
		rl:    rl,
		ring:  ring,
		web:   webui.Handler(cfg.WebDir),
	}

	// Public auth.
	r.mux.HandleFunc("POST /api/login", r.loginHandler)
	r.mux.HandleFunc("POST /api/logout", r.logoutHandler)

	// Authenticated API (session + CSRF).
	auth := sessionMiddleware(sm, http.HandlerFunc(r.apiHandler))
	r.mux.Handle("/api/", csrfMiddleware(auth))

	// SSE stream (authenticated, no CSRF since it's GET).
	r.mux.Handle("GET /api/stream", sessionMiddleware(sm, http.HandlerFunc(r.hub.ServeHTTP)))

	// Static fallback (no auth for the app shell; API paths are protected).
	r.mux.HandleFunc("/", r.staticHandler)

	return r
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// apiHandler dispatches authenticated API endpoints.
func (r *Router) apiHandler(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/api/session":
		r.writeJSON(w, map[string]bool{"authenticated": true})
	case "/api/overview":
		r.handleOverview(w, req)
	case "/api/orgchart":
		r.handleOrgChart(w, req)
	case "/api/cost":
		r.handleCost(w, req)
	case "/api/activity":
		r.handleActivity(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (r *Router) loginHandler(w http.ResponseWriter, req *http.Request) {
	var reqBody struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	ip := clientIP(req)
	if !r.rl.Allow(ip) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}

	if subtle.ConstantTimeCompare([]byte(reqBody.Password), []byte(r.cfg.DashboardPassword)) != 1 {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	token := r.sm.Create()
	setSessionCookie(w, token, isSecure(req))
	r.writeJSON(w, map[string]bool{"authenticated": true})
}

func (r *Router) logoutHandler(w http.ResponseWriter, req *http.Request) {
	cookie, _ := req.Cookie(sessionCookieName)
	if cookie != nil {
		r.sm.Delete(cookie.Value)
	}
	clearSessionCookie(w)
	r.writeJSON(w, map[string]bool{"authenticated": false})
}

func (r *Router) handleOverview(w http.ResponseWriter, req *http.Request) {
	ov, err := aggregate.Overview(req.Context(), r.state, r.ws, r.http, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.writeJSON(w, ov)
}

func (r *Router) handleOrgChart(w http.ResponseWriter, req *http.Request) {
	dag, err := aggregate.OrgChart(req.Context(), r.ws, r.http)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.writeJSON(w, dag)
}

func (r *Router) handleCost(w http.ResponseWriter, req *http.Request) {
	cs, err := aggregate.Cost(req.Context(), r.http, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.writeJSON(w, cs)
}

func (r *Router) handleActivity(w http.ResponseWriter, req *http.Request) {
	limit := 100
	if raw := req.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	items := []activity.ActivityItem{}
	if r.ring != nil {
		items = r.ring.List(limit)
	}
	r.writeJSON(w, map[string]any{"items": items})
}

// staticHandler serves the embedded UI or a 404 hint. API paths never fall
// through to the SPA.
func (r *Router) staticHandler(w http.ResponseWriter, req *http.Request) {
	if strings.HasPrefix(req.URL.Path, "/api/") {
		http.NotFound(w, req)
		return
	}
	r.web.ServeHTTP(w, req)
}

func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i >= 0 {
		ip = ip[:i]
	}
	return ip
}

func isSecure(req *http.Request) bool {
	return req.TLS != nil
}
