package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCSRFMissingOrigin(t *testing.T) {
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("POST without Origin = %d, want 403", w.Code)
	}
}

func TestCSRFWrongOrigin(t *testing.T) {
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("POST with wrong Origin = %d, want 403", w.Code)
	}
}

func TestCSRFMatchingOrigin(t *testing.T) {
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST with matching Origin = %d, want 200", w.Code)
	}
}

func TestCSRFGetNoOrigin(t *testing.T) {
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET without Origin = %d, want 200", w.Code)
	}
}

func TestSessionMiddlewareUnauthenticated(t *testing.T) {
	sm := NewSessionManager(time.Hour)
	handler := sessionMiddleware(sm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d, want 401", w.Code)
	}
}

func TestSessionMiddlewareAuthenticated(t *testing.T) {
	sm := NewSessionManager(time.Hour)
	tok := sm.Create()
	handler := sessionMiddleware(sm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated = %d, want 200", w.Code)
	}
}

func TestOriginAllowed(t *testing.T) {
	if !originAllowed("https://example.com", "example.com") {
		t.Error("example.com should be allowed")
	}
	if originAllowed("https://evil.com", "example.com") {
		t.Error("evil.com should not be allowed")
	}
	if !originAllowed("http://example.com:18795", "example.com:18795") {
		t.Error("example.com:18795 should be allowed")
	}
}
