// Package webui serves the embedded dashboard SPA when the dashboardui build
// tag is set, or falls back to a --web-dir hint when no UI is embedded.
package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

var zeroTime time.Time

// Handler returns an HTTP handler that serves the SPA from webDir when set,
// otherwise from the embedded assets. Unknown non-asset paths fall back to
// index.html so client-side routes work on reload.
func Handler(webDir string) http.Handler {
	if webDir != "" {
		return handlerForFS(os.DirFS(webDir))
	}
	assets := Assets()
	if assets == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "dashboard UI not built; rebuild with -tags dashboardui or pass --web-dir", http.StatusNotFound)
		})
	}
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded dashboard UI is invalid", http.StatusInternalServerError)
		})
	}
	return handlerForFS(dist)
}

func handlerForFS(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		requested := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requested == "." || requested == "" {
			serveIndex(w, r, root)
			return
		}

		if data, err := fs.ReadFile(root, requested); err == nil {
			http.ServeContent(w, r, path.Base(requested), zeroTime, bytes.NewReader(data))
			return
		}

		// Client-side route: no extension -> index.html. Asset-like misses 404.
		if path.Ext(requested) == "" {
			serveIndex(w, r, root)
			return
		}
		http.NotFound(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "dashboard UI index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
