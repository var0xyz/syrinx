//go:build !ops

package main

import (
	"testing"
	"time"
)

// Golden bytes shared with scripts/test-follow-payload.mjs.
func TestBuildFollowPayloads(t *testing.T) {
	user := string(buildFollowUserPayload("ann@home1234", "bob@peer5678", "ann@home1234/fp", false))
	wantUser := `{"follow":false,"followeeID":"bob@peer5678","followerID":"ann@home1234","keyID":"ann@home1234/fp","type":"follow"}`
	if user != wantUser {
		t.Fatalf("user payload\n got %s\nwant %s", user, wantUser)
	}
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	server := string(buildFollowServerPayload("ann@home1234", "bob@peer5678", true, "SERVERFP", "c2ln", at))
	wantServer := `{"follow":true,"followeeID":"bob@peer5678","followerID":"ann@home1234","serverKeyFingerprint":"SERVERFP",` +
		`"signedAt":"2026-10-23T12:00:00Z","type":"follow","userSignature":"c2ln"}`
	if server != wantServer {
		t.Fatalf("server payload\n got %s\nwant %s", server, wantServer)
	}
}
