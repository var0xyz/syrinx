//go:build !ops

package main

import (
	"context"
	"testing"
)

func TestInitServerKeepsFrontendURLInSync(t *testing.T) {
	db := newTestDatabase(t, InitDB)
	ds := NewDataService(db, "test")
	ctx := context.Background()

	selfFrontendURL := func() string {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT frontend_url FROM servers WHERE self = TRUE`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if err := ds.InitServer(ctx, false, "https://api.test.example", "https://test.example"); err != nil {
		t.Fatal(err)
	}
	if got := selfFrontendURL(); got != "https://test.example" {
		t.Fatalf("first boot frontend_url = %q, want %q", got, "https://test.example")
	}

	if err := ds.InitServer(ctx, false, "https://api.test.example", "https://moved.example"); err != nil {
		t.Fatal(err)
	}
	if got := selfFrontendURL(); got != "https://moved.example" {
		t.Fatalf("frontend_url after ALLOWED_ORIGIN change = %q, want %q", got, "https://moved.example")
	}
}

func TestFrontendURLFromOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		"https://test.example":    "https://test.example",
		" https://test.example/ ": "https://test.example",
		"":                        "",
		"  ":                      "",
	} {
		if got := frontendURLFromOrigin(origin); got != want {
			t.Errorf("frontendURLFromOrigin(%q) = %q, want %q", origin, got, want)
		}
	}
}
