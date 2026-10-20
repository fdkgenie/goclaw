// Package server wires the dashboard HTTP API: password auth, CSRF Origin
// checks, rate limiting, and API routing. Data is fetched via the goclaw
// clients (read-only, allow-listed); the server never touches the database.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
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

	// Public auth. Both routes are state-changing (they mint and clear the
	// session cookie), so they validate the Origin header even though they
	// sit outside the /api/ CSRF chain.
	r.mux.Handle("POST /api/login", originMiddleware(http.HandlerFunc(r.loginHandler)))
	r.mux.Handle("POST /api/logout", originMiddleware(http.HandlerFunc(r.logoutHandler)))

	// Session state stays available while degraded so the SPA can decide
	// whether to show the login screen instead of the data shell.
	r.mux.Handle("GET /api/session", sessionMiddleware(sm, http.HandlerFunc(r.handleSession)))

	// Authenticated data API (session + CSRF + fail-closed degrade gate).
	auth := sessionMiddleware(sm, degradedMiddleware(http.HandlerFunc(r.apiHandler)))
	r.mux.Handle("/api/", csrfMiddleware(auth))

	// SSE stream (authenticated, no CSRF since it's GET; fail-closed gate).
	r.mux.Handle("GET /api/stream", sessionMiddleware(sm, degradedMiddleware(http.HandlerFunc(r.hub.ServeHTTP))))

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

func (r *Router) handleSession(w http.ResponseWriter, _ *http.Request) {
	r.writeJSON(w, map[string]bool{"authenticated": true})
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
	ctx := req.Context()
	// The team roster is resolved first: the task KPIs are queried per team,
	// so it must be known before the aggregates are computed.
	teams, resolve := r.teams(ctx)
	ov, err := aggregate.Overview(ctx, r.state, r.ws, r.http, teams, resolve)
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
	q := aggregate.CostQuery{}
	if teamID := strings.TrimSpace(req.URL.Query().Get("team_id")); teamID != "" {
		q.TeamID = teamID
	}
	cs, err := aggregate.Cost(req.Context(), r.http, r.teamResolver(req.Context()), q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.writeJSON(w, cs)
}

// teams loads the team roster and the matching display-name resolver.
//
// The roster is best effort: when the gateway team list is unavailable the
// overview still reports the live and cost KPIs, the task KPIs cover no team
// and the cost breakdown keeps the raw team ids.
func (r *Router) teams(ctx context.Context) ([]aggregate.TeamRef, aggregate.TeamResolver) {
	if r.ws == nil {
		return nil, nil
	}
	teams, err := aggregate.ListTeams(ctx, r.ws)
	if err != nil {
		slog.Warn("dashboard team list failed", "err", err)
		return nil, nil
	}
	return teams, aggregate.TeamResolverFor(teams)
}

// teamResolver maps a team id to its display name for the cost breakdown.
// It is best effort: when the gateway team list is unavailable the rows keep
// their raw team id.
func (r *Router) teamResolver(ctx context.Context) aggregate.TeamResolver {
	_, resolve := r.teams(ctx)
	return resolve
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
