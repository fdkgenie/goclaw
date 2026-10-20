package server

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
)

// stringReader returns a reader over a literal body.
func stringReader(s string) io.Reader {
	return strings.NewReader(s)
}

// cookieRouter builds a router with the given security configuration.
func cookieRouter(cfg *config.Config) *Router {
	state := live.NewState()
	return NewRouter(
		cfg,
		state,
		nil,
		nil,
		live.NewHub(state),
		NewSessionManager(time.Hour),
		NewRateLimiter(rate.Every(time.Minute), 100),
		activity.NewRingBuffer(),
	)
}

// loginOver performs a login request and returns the recorder.
func loginOver(t *testing.T, r *Router, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if req.Body == nil {
		req.Body = http.NoBody
	}
	req.Header.Set("Content-Type", "application/json")
	if req.Host == "" {
		req.Host = "example.com"
	}
	if req.Header.Get("Origin") == "" {
		req.Header.Set("Origin", "https://"+req.Host)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// sessionCookie returns the session cookie of a response, or nil.
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// TestSessionCookieSecureFlags pins the Secure flag of the session cookie.
// The dashboard is usually served behind a reverse proxy that terminates
// TLS, so the plain HTTP request carries the protocol of the proxy in a
// header. That header is only trusted when the operator declares the proxy
// trusted.
func TestSessionCookieSecureFlags(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.Config
		tls          bool
		forwarded    string
		wantStatus   int
		wantSecure   bool
		wantNoCookie bool
	}{
		{
			name:       "plain http is not secure",
			cfg:        &config.Config{DashboardPassword: "secret"},
			wantStatus: http.StatusOK,
			wantSecure: false,
		},
		{
			name:       "tls is secure",
			cfg:        &config.Config{DashboardPassword: "secret"},
			tls:        true,
			wantStatus: http.StatusOK,
			wantSecure: true,
		},
		{
			name:       "untrusted proxy header is ignored",
			cfg:        &config.Config{DashboardPassword: "secret"},
			forwarded:  "https",
			wantStatus: http.StatusOK,
			wantSecure: false,
		},
		{
			name:       "trusted proxy header is honoured",
			cfg:        &config.Config{DashboardPassword: "secret", TrustedProxy: true},
			forwarded:  "https",
			wantStatus: http.StatusOK,
			wantSecure: true,
		},
		{
			name:       "trusted proxy without the header is not secure",
			cfg:        &config.Config{DashboardPassword: "secret", TrustedProxy: true},
			wantStatus: http.StatusOK,
			wantSecure: false,
		},
		{
			name:       "trusted proxy with a plain header is not secure",
			cfg:        &config.Config{DashboardPassword: "secret", TrustedProxy: true},
			forwarded:  "http",
			wantStatus: http.StatusOK,
			wantSecure: false,
		},
		{
			name:         "production refuses a plain http login",
			cfg:          &config.Config{DashboardPassword: "secret", Production: true},
			wantStatus:   http.StatusBadRequest,
			wantNoCookie: true,
		},
		{
			name:       "production accepts a tls login",
			cfg:        &config.Config{DashboardPassword: "secret", Production: true},
			tls:        true,
			wantStatus: http.StatusOK,
			wantSecure: true,
		},
		{
			name:       "production accepts a trusted proxy login",
			cfg:        &config.Config{DashboardPassword: "secret", Production: true, TrustedProxy: true},
			forwarded:  "https",
			wantStatus: http.StatusOK,
			wantSecure: true,
		},
		{
			name:         "production refuses an untrusted proxy header",
			cfg:          &config.Config{DashboardPassword: "secret", Production: true},
			forwarded:    "https",
			wantStatus:   http.StatusBadRequest,
			wantNoCookie: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := cookieRouter(tc.cfg)
			req := httptest.NewRequest(http.MethodPost, "/api/login", stringReader(`{"password":"secret"}`))
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}

			w := loginOver(t, router, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("login status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			cookie := sessionCookie(t, w)
			if tc.wantNoCookie {
				if cookie != nil {
					t.Fatal("a session cookie was issued over plain HTTP in production mode")
				}
				return
			}
			if cookie == nil {
				t.Fatal("expected a session cookie")
			}
			if cookie.Secure != tc.wantSecure {
				t.Errorf("cookie Secure = %v, want %v", cookie.Secure, tc.wantSecure)
			}
			if !cookie.HttpOnly {
				t.Error("cookie must stay HttpOnly")
			}
			if cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("cookie SameSite = %v, want Strict", cookie.SameSite)
			}
		})
	}
}

// TestLogoutClearsCookieWithTheSameFlags makes sure the logout cookie can
// actually clear the session cookie: a Secure cookie cannot be cleared by a
// plain one.
func TestLogoutClearsCookieWithTheSameFlags(t *testing.T) {
	router := cookieRouter(&config.Config{DashboardPassword: "secret"})
	w := loginOver(t, router, httptest.NewRequest(http.MethodPost, "/api/login", stringReader(`{"password":"secret"}`)))
	cookie := sessionCookie(t, w)
	if cookie == nil {
		t.Fatal("expected a session cookie")
	}
	if cookie.Secure {
		t.Fatal("expected a plain HTTP login to issue a non-Secure cookie")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(cookie)
	w = loginOver(t, router, req)
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", w.Code)
	}
	cleared := sessionCookie(t, w)
	if cleared == nil {
		t.Fatal("expected a cookie clearing the session")
	}
	if cleared.Secure {
		t.Error("the clearing cookie must not be Secure, the session cookie was not")
	}
}
