package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
)

func testRouter(t *testing.T) *Router {
	t.Helper()
	router, _, _ := testRouterWithHub(t)
	return router
}

func testRouterWithHub(t *testing.T) (*Router, *live.Hub, *SessionManager) {
	t.Helper()
	// The degraded gate is process-wide; make sure a previous test cannot
	// leak an open breaker into this one.
	goclaw.Reset()
	t.Cleanup(goclaw.Reset)
	cfg := &config.Config{DashboardPassword: "secret"}
	sm := NewSessionManager(time.Hour)
	rl := NewRateLimiter(rate.Every(time.Minute), 5)
	state := live.NewState()
	hub := live.NewHub(state)
	return NewRouter(cfg, state, nil, nil, hub, sm, rl, activity.NewRingBuffer()), hub, sm
}

func streamStatus(t *testing.T, router *Router, cookie *http.Cookie) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

func TestStaticServesSPAAndAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa-shell</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('spa')"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	cfg := &config.Config{DashboardPassword: "secret", WebDir: dir}
	state := live.NewState()
	router := NewRouter(cfg, state, nil, nil, live.NewHub(state), NewSessionManager(time.Hour), NewRateLimiter(rate.Every(time.Minute), 5), activity.NewRingBuffer())

	tests := []struct {
		path string
		want string
	}{
		{path: "/", want: "spa-shell"},
		{path: "/teams/alpha", want: "spa-shell"}, // SPA fallback
		{path: "/assets/app.js", want: "console.log('spa')"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", tc.path, w.Code)
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s body = %q, want %q", tc.path, w.Body.String(), tc.want)
		}
	}

	// Unknown API paths never fall through to the SPA.
	sm := router.sm
	tok := sm.Create()
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want 404", w.Code)
	}
	if strings.Contains(w.Body.String(), "spa-shell") {
		t.Fatal("unknown API path was served the SPA shell")
	}
}

func TestActivityEndpointReturnsRingItems(t *testing.T) {
	ring := activity.NewRingBuffer()
	ring.Add(activity.ActivityItem{
		ID:      "e1",
		Type:    activity.TypeAgentEvent,
		Summary: "run.started",
	})
	state := live.NewState()
	sm := NewSessionManager(time.Hour)
	router := NewRouter(&config.Config{DashboardPassword: "secret"}, state, nil, nil, live.NewHub(state), sm, NewRateLimiter(rate.Every(time.Minute), 5), ring)

	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sm.Create()})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("activity status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "run.started") {
		t.Fatalf("activity body = %q, want ring item", w.Body.String())
	}
}

func TestStreamRequiresSession(t *testing.T) {
	t.Run("anonymous", func(t *testing.T) {
		router, _, _ := testRouterWithHub(t)
		if got := streamStatus(t, router, nil); got != http.StatusUnauthorized {
			t.Fatalf("anonymous status = %d, want 401", got)
		}
	})

	t.Run("invalid session", func(t *testing.T) {
		router, _, _ := testRouterWithHub(t)
		cookie := &http.Cookie{Name: sessionCookieName, Value: "not-a-session"}
		if got := streamStatus(t, router, cookie); got != http.StatusUnauthorized {
			t.Fatalf("invalid session status = %d, want 401", got)
		}
	})

	t.Run("expired session", func(t *testing.T) {
		expired := NewSessionManager(-time.Second)
		tok := expired.Create()
		state := live.NewState()
		router := NewRouter(&config.Config{DashboardPassword: "secret"}, state, nil, nil, live.NewHub(state), expired, NewRateLimiter(rate.Every(time.Minute), 5), nil)
		cookie := &http.Cookie{Name: sessionCookieName, Value: tok}
		if got := streamStatus(t, router, cookie); got != http.StatusUnauthorized {
			t.Fatalf("expired session status = %d, want 401", got)
		}
	})
}

func TestStreamAuthenticatedReceivesFrame(t *testing.T) {
	router, hub, sm := testRouterWithHub(t)
	tok := sm.Create()

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	lines := make(chan string, 32)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		close(lines)
	}()

	waitFor := func(substr string) string {
		t.Helper()
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %q", substr)
				}
				if strings.Contains(line, substr) {
					return line
				}
			case <-ctx.Done():
				t.Fatalf("timeout waiting for %q", substr)
			}
		}
	}

	waitFor(`"snapshot"`)
	hub.ApplyAndPublish(goclaw.AgentEventPayload{RunID: "r1", AgentID: "a1", Event: "run.started"})
	line := waitFor(`"delta"`)
	if !strings.Contains(line, `run.started`) {
		t.Fatalf("delta frame missing event payload: %s", line)
	}
}

// TestDegradedGateFailsClosedThenRecovers asserts the fail-closed contract:
// while the tenant canary is unverified the data API and the SSE stream both
// answer 503, while the session endpoint stays reachable so the SPA can
// render its shell.
func TestDegradedGateFailsClosedThenRecovers(t *testing.T) {
	router, _, sm := testRouterWithHub(t)
	tok := sm.Create()
	cookie := &http.Cookie{Name: sessionCookieName, Value: tok}

	goclaw.SetDegraded("gateway disconnected")

	// Data API fails closed.
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded activity status = %d, want 503", w.Code)
	}

	// SSE stream fails closed.
	if got := streamStatus(t, router, cookie); got != http.StatusServiceUnavailable {
		t.Fatalf("degraded stream status = %d, want 503", got)
	}

	// The session endpoint stays reachable: the SPA must still know it is
	// authenticated so it can show the shell instead of the login screen.
	req = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("degraded session status = %d, want 200", w.Code)
	}

	// Unauthenticated callers still get 401, not 503: the gate is an
	// additional check, not a replacement for the session check.
	if got := streamStatus(t, router, nil); got != http.StatusUnauthorized {
		t.Fatalf("degraded anonymous stream status = %d, want 401", got)
	}

	// After the canary re-verifies, the gate opens again.
	goclaw.Reset()
	req = httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("recovered activity status = %d, want 200", w.Code)
	}
}

func postJSON(t *testing.T, r *Router, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLoginCorrectPassword(t *testing.T) {
	r := testRouter(t)
	w := postJSON(t, r, "/api/login", `{"password":"secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login correct = %d, want 200", w.Code)
	}
	cookie := w.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("expected session cookie")
	}
	if cookie[0].Name != sessionCookieName {
		t.Errorf("cookie name = %q, want %q", cookie[0].Name, sessionCookieName)
	}
	if cookie[0].HttpOnly != true {
		t.Error("expected HttpOnly cookie")
	}
	if strings.Contains(w.Body.String(), cookie[0].Value) {
		t.Fatal("login response body leaked the session token")
	}
	if !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatalf("login body = %q, want authenticated state", w.Body.String())
	}
}

func TestSessionEndpointWithCookie(t *testing.T) {
	router, _, sm := testRouterWithHub(t)
	tok := sm.Create()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatalf("session body = %q", w.Body.String())
	}
}

func TestSessionEndpointWithoutCookie(t *testing.T) {
	router, _, _ := testRouterWithHub(t)
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session status = %d, want 401", w.Code)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	r := testRouter(t)
	w := postJSON(t, r, "/api/login", `{"password":"wrong"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login wrong = %d, want 401", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	r := testRouter(t)
	for i := 0; i < 5; i++ {
		w := postJSON(t, r, "/api/login", `{"password":"wrong"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, w.Code)
		}
	}
	w := postJSON(t, r, "/api/login", `{"password":"wrong"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt = %d, want 429", w.Code)
	}
}

func TestLoginMissingPassword(t *testing.T) {
	r := testRouter(t)
	w := postJSON(t, r, "/api/login", `{}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty password = %d, want 401", w.Code)
	}
}

func TestLogout(t *testing.T) {
	r := testRouter(t)
	// login first
	w := postJSON(t, r, "/api/login", `{"password":"secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d", w.Code)
	}
	tok := w.Result().Cookies()[0].Value

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200", w2.Code)
	}
	if w2.Header().Get("Set-Cookie") == "" {
		t.Error("expected logout to clear session cookie")
	}
}
