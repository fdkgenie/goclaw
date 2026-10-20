package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

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
	cfg := &config.Config{DashboardPassword: "secret"}
	sm := NewSessionManager(time.Hour)
	rl := NewRateLimiter(rate.Every(time.Minute), 5)
	state := live.NewState()
	hub := live.NewHub(state)
	return NewRouter(cfg, state, nil, nil, hub, sm, rl), hub, sm
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
		router := NewRouter(&config.Config{DashboardPassword: "secret"}, state, nil, nil, live.NewHub(state), expired, NewRateLimiter(rate.Every(time.Minute), 5))
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
