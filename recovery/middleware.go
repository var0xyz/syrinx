package recovery

import (
	"net/http"
	"strings"
)

type errorMessage struct {
	Error string `json:"error"`
}

// AllowedDuringImport reports whether path may be used while the caller is in
// ongoing_recoveries. path is the request URL path (e.g. /api/server/info).
func AllowedDuringImport(path string) bool {
	if path == "/api/server/info" {
		return true
	}
	if path == "/api/users/status" {
		return true
	}
	if strings.HasPrefix(path, "/api/recovery/") {
		return true
	}
	if strings.HasPrefix(path, "/api/server/keys/") {
		return true
	}
	return false
}

// Middleware returns the import-gate middleware. userIDKey is the context key
// signature-auth uses for the authenticated user id. isOngoing reports whether
// that user is mid-import. Authenticated users mid-import get 403 on
// non-allowlisted paths. OPTIONS always passes.
func Middleware(userIDKey any, isOngoing func(userID string) (bool, error)) func(http.Handler) http.Handler {
	return middleware(userIDKey, isOngoing)
}

func middleware(userIDKey any, isOngoing func(userID string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			userID, ok := r.Context().Value(userIDKey).(string)
			if !ok || userID == "" {
				next.ServeHTTP(w, r)
				return
			}

			if AllowedDuringImport(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ongoing, err := isOngoing(userID)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if ongoing {
				writeJSON(w, http.StatusForbidden, errorMessage{Error: "Finish recovery import first."})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
