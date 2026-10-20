package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetsFallbackNil(t *testing.T) {
	if Assets() != nil {
		t.Fatal("expected Assets() to be nil without the dashboardui build tag")
	}
}

func testFS() fs.FS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<html>dashboard shell</html>")},
		"assets/app.js": {Data: []byte("console.log('ok')")},
	}
}

func TestHandlerServesIndexAndAssets(t *testing.T) {
	h := handlerForFS(testFS())

	tests := []struct {
		path        string
		wantStatus  int
		wantBody    string
		wantContent string
	}{
		{path: "/", wantStatus: http.StatusOK, wantBody: "dashboard shell", wantContent: "text/html"},
		{path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log", wantContent: "javascript"},
		{path: "/teams/overview", wantStatus: http.StatusOK, wantBody: "dashboard shell", wantContent: "text/html"},
		{path: "/assets/missing.js", wantStatus: http.StatusNotFound, wantBody: "404"},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.wantStatus {
			t.Errorf("%s status = %d, want %d", tc.path, w.Code, tc.wantStatus)
		}
		if !strings.Contains(w.Body.String(), tc.wantBody) {
			t.Errorf("%s body = %q, want %q", tc.path, w.Body.String(), tc.wantBody)
		}
		if tc.wantContent != "" && !strings.Contains(w.Header().Get("Content-Type"), tc.wantContent) {
			t.Errorf("%s content-type = %q, want %q", tc.path, w.Header().Get("Content-Type"), tc.wantContent)
		}
	}
}

func TestHandlerDoesNotShadowAPI(t *testing.T) {
	h := handlerForFS(testFS())
	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("api path status = %d, want 404", w.Code)
	}
	if strings.Contains(w.Body.String(), "dashboard shell") {
		t.Fatal("API path was served the SPA index")
	}
}
