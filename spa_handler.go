//go:build !ops && !ripplescleanup

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
			// Hashed build assets must 404, not fall back to the SPA shell:
			// serving index.html (text/html) for a missing chunk is what
			// produces "Failed to load module script ... MIME type of
			// text/html" after a deploy replaces /_app/'s hashed filenames —
			// and the reload SvelteKit fires in response just re-fetches the
			// same stale shell, looping forever.
			if strings.HasPrefix(urlPath, "/_app/") {
				http.NotFound(w, r)
				return
			}
			// Client route: fall back to the SPA shell.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
