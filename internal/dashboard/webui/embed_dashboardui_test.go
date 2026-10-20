//go:build dashboardui

package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAssetsEmbedded verifies that the dashboardui build embeds the bundle.
// The stub build (no tag) returns nil, so this test lives behind the tag.
func TestAssetsEmbedded(t *testing.T) {
	assets := Assets()
	if assets == nil {
		t.Fatal("expected embedded assets with the dashboardui build tag")
	}
	if _, err := fs.Stat(assets, "dist"); err != nil {
		t.Fatalf("stat dist: %v", err)
	}
}

// TestEmbeddedBundleIsUsable verifies the embedded dashboard bundle. The
// bundle is only populated when the SPA was built before the binary was
// linked (make dashboard-build), so the test skips when it is empty and
// asserts the served shell otherwise.
func TestEmbeddedBundleIsUsable(t *testing.T) {
	assets := Assets()
	if assets == nil {
		t.Fatal("expected embedded assets with the dashboardui build tag")
	}
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		t.Fatalf("sub dist: %v", err)
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Skip("dashboard SPA not built; run make dashboard-build to exercise the embedded bundle")
	}

	h := Handler("")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("index body = %q, want the SPA shell", w.Body.String())
	}

	// A client-side route must fall back to the shell, not 404.
	req = httptest.NewRequest(http.MethodGet, "/teams/alpha", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SPA route status = %d, want 200", w.Code)
	}

	// API paths must never be shadowed by the shell.
	req = httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("api path status = %d, want 404", w.Code)
	}
}
