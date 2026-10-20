package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginRequest issues a POST /api/login with a controlled Origin header.
func loginRequest(t *testing.T, r *Router, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "example.com"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestLoginSameOriginAllowed proves a same-origin login still works after
// the Origin check was added.
func TestLoginSameOriginAllowed(t *testing.T) {
	r := testRouter(t)
	w := loginRequest(t, r, `{"password":"secret"}`, "https://example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin login = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("expected a session cookie")
	}
}

// TestLoginMissingOriginRejected proves a login without an Origin header is
// rejected: the header is mandatory and a missing one fails closed.
func TestLoginMissingOriginRejected(t *testing.T) {
	r := testRouter(t)
	w := loginRequest(t, r, `{"password":"secret"}`, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("login without Origin = %d, want 403", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("rejected login must not mint a session cookie")
	}
}

// TestLoginForeignOriginRejected proves a cross-origin login is rejected and
// does not mint a session.
func TestLoginForeignOriginRejected(t *testing.T) {
	r := testRouter(t)
	w := loginRequest(t, r, `{"password":"secret"}`, "https://evil.example")
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin login = %d, want 403", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("rejected login must not mint a session cookie")
	}
}

// TestLoginForeignOriginDoesNotConsumeRateLimit proves the Origin check runs
// before the rate limiter: a rejected cross-origin attempt must not burn a
// login attempt of the legitimate client.
func TestLoginForeignOriginDoesNotConsumeRateLimit(t *testing.T) {
	r := testRouter(t)
	for i := 0; i < 3; i++ {
		if w := loginRequest(t, r, `{"password":"secret"}`, "https://evil.example"); w.Code != http.StatusForbidden {
			t.Fatalf("foreign-origin attempt %d = %d, want 403", i+1, w.Code)
		}
	}
	w := loginRequest(t, r, `{"password":"secret"}`, "https://example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin login after rejected attempts = %d, want 200", w.Code)
	}
}

// TestLogoutForeignOriginRejected proves the logout route is guarded too:
// it clears the session cookie, so it is state-changing.
func TestLogoutForeignOriginRejected(t *testing.T) {
	r := testRouter(t)
	login := loginRequest(t, r, `{"password":"secret"}`, "https://example.com")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", login.Code)
	}
	tok := login.Result().Cookies()[0].Value

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	req.Host = "example.com"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin logout = %d, want 403", w.Code)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("rejected logout must not clear the session cookie")
	}
}

// TestLogoutSameOriginAllowed proves the logout route still works for a
// same-origin request.
func TestLogoutSameOriginAllowed(t *testing.T) {
	r := testRouter(t)
	login := loginRequest(t, r, `{"password":"secret"}`, "https://example.com")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", login.Code)
	}
	tok := login.Result().Cookies()[0].Value

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin logout = %d, want 200", w.Code)
	}
}
