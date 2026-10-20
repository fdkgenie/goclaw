package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
)

func testRouter(t *testing.T) *Router {
	t.Helper()
	cfg := &config.Config{DashboardPassword: "secret"}
	sm := NewSessionManager(time.Hour)
	rl := NewRateLimiter(rate.Every(time.Minute), 5)
	return NewRouter(cfg, nil, nil, nil, nil, sm, rl)
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
