//go:build !ops

package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVerificationWindow_Lifecycle(t *testing.T) {
	db, rs, _ := newTeardownTestService(t)
	ctx := context.Background()
	user := string(canonicalID(teardownHomeID, "sam"))
	insertTeardownIdentity(t, db, user, teardownHomeID)
	if _, err := db.Exec(`INSERT INTO users (id) VALUES ($1)`, user); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ds := rs.db
	open := func(id string, at time.Time) bool {
		t.Helper()
		ok, err := ds.OpenVerificationWindow(ctx, user, id, at)
		if err != nil {
			t.Fatalf("OpenVerificationWindow: %v", err)
		}
		return ok
	}
	now := time.Now().UTC().Truncate(time.Second)

	isOpen := func(at time.Time) bool {
		t.Helper()
		open, err := ds.VerificationWindowOpen(ctx, user, at)
		if err != nil {
			t.Fatalf("VerificationWindowOpen: %v", err)
		}
		return open
	}
	ping := func(id string, at time.Time) bool {
		t.Helper()
		ok, err := ds.PingVerificationWindow(ctx, user, id, at)
		if err != nil {
			t.Fatalf("PingVerificationWindow: %v", err)
		}
		return ok
	}

	if isOpen(now) {
		t.Fatal("open before any window was opened")
	}
	if open(uuid.NewString(), now) {
		t.Fatal("opened a window for a user who isn't online")
	}
	if _, err := db.Exec(`INSERT INTO online_users (user_id) VALUES ($1)`, user); err != nil {
		t.Fatalf("mark user online: %v", err)
	}
	first := uuid.NewString()
	if !open(first, now) {
		t.Fatal("could not open a window for an online user")
	}
	if !isOpen(now) || !ping(first, now.Add(30*time.Second)) {
		t.Fatal("a fresh window should be open and pingable")
	}

	// Opening another closes the first: one window at a time.
	second := uuid.NewString()
	if !open(second, now.Add(40*time.Second)) {
		t.Fatal("could not reopen")
	}
	if ping(first, now.Add(45*time.Second)) {
		t.Fatal("the replaced window still answered a ping")
	}

	// A ping more than a minute late finds the window expired.
	if isOpen(now.Add(40*time.Second + verificationWindowTTL + time.Second)) {
		t.Fatal("still open a minute after the last ping")
	}
	if ping(second, now.Add(40*time.Second+verificationWindowTTL+time.Second)) {
		t.Fatal("an expired window was revived by a ping")
	}

	third := uuid.NewString()
	if !open(third, now) {
		t.Fatal("could not open a third window")
	}
	if err := ds.CloseVerificationWindow(ctx, user, third); err != nil {
		t.Fatalf("close: %v", err)
	}
	if isOpen(now) {
		t.Fatal("still open after closing")
	}

	// Going offline closes the window without any explicit call.
	if !open(uuid.NewString(), now) {
		t.Fatal("could not open a fourth window")
	}
	if _, err := db.Exec(`DELETE FROM online_users WHERE user_id = $1`, user); err != nil {
		t.Fatalf("mark user offline: %v", err)
	}
	if isOpen(now) {
		t.Fatal("still open after the user went offline")
	}
}
