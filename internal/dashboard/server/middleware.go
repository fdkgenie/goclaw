package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const sessionCookieName = "goclaw_dashboard_session"

// csrfMiddleware verifies the Origin header on POST requests to prevent
// cross-site request forgery. The Origin must match the request host.
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin == "" {
				http.Error(w, "missing Origin header", http.StatusForbidden)
				return
			}
			if !originAllowed(origin, r.Host) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether origin (a full URL like https://host:port) is
// allowed for the given request host.
func originAllowed(origin, host string) bool {
	rest := origin
	if i := strings.Index(origin, "://"); i >= 0 {
		rest = origin[i+3:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return rest == host
}

// sessionMiddleware requires a valid session cookie for authenticated API
// paths, except /api/login and /api/logout.
func sessionMiddleware(sm *SessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/login" || path == "/api/logout" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !sm.Validate(cookie.Value) {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// degradedMiddleware fails closed while the tenant canary has not re-verified
// after a gateway disconnect. Data APIs and the SSE stream return 503 until the
// breaker resets.
func degradedMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if goclaw.IsDegraded() {
			http.Error(w, "dashboard degraded: tenant verification pending", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clearSessionCookie clears the session cookie on the client.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	})
}

// setSessionCookie sets the session cookie on the client.
func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	})
}
