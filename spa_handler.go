//go:build !ops

package main

import (
	"net/http"
	"os"
	"path"
	"strings"
)

// spaHandler serves a SvelteKit static build with SPA fallback to index.html
// for client-side routes. API and WebSocket must be registered before this.
func spaHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := path.Clean("/" + r.URL.Path)
		// Never cache the service worker script — stale or flip-flopping bytes
		// behind a proxy during deploy cause endless update/reload cycles.
		if urlPath == "/service-worker.js" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		if urlPath != "/" {
			rel := strings.TrimPrefix(urlPath, "/")
			if fi, err := os.Stat(path.Join(root, rel)); err == nil && !fi.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
			// Client route or missing asset: fall back to the SPA shell.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
