package recovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowedDuringImport(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/server/info", true},
		{"/api/recovery/identity/claim", true},
		{"/api/recovery/", true},
		{"/api/server/keys/ABC", true},
		{"/api/server/keys/", true},
		{"/api/users/me", false},
		{"/api/server/inf", false},
		{"/api/recovery", false},
		{"/api/health", false},
	}
	for _, tt := range tests {
		if got := AllowedDuringImport(tt.path); got != tt.want {
			t.Errorf("AllowedDuringImport(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	type ctxKey struct{}
	key := ctxKey{}

	ongoing := map[string]bool{"user-ongoing": true}
	mw := middleware(key, func(userID string) (bool, error) {
		return ongoing[userID], nil
	})

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(okHandler)

	withUID := func(r *http.Request, uid string) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), key, uid))
	}

	t.Run("OPTIONS always allowed", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodOptions, "/api/users/me", nil), "user-ongoing")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("unauthenticated passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("ongoing blocked on non-allowlist", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/users/me", nil), "user-ongoing")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
	})

	t.Run("ongoing allowed on server info", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/server/info", nil), "user-ongoing")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("ongoing allowed on recovery paths", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/recovery/complete", nil), "user-ongoing")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("finished user not gated", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/users/me", nil), "user-done")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})
}
