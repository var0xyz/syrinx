//go:build !ops

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSpaHandlerServesFileAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("shell"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := spaHandler(dir)

	t.Run("existing file", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if got := rr.Body.String(); got != "hi" {
			t.Fatalf("body = %q, want hi", got)
		}
	})

	t.Run("spa fallback", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/reeds", nil)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if got := rr.Body.String(); got != "shell" {
			t.Fatalf("body = %q, want shell", got)
		}
	})
}
