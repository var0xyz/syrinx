//go:build !ops

package main

import (
	"context"
	"testing"
	"time"
)

// The server refuses to start until its identity is exported, and again
// after a new signing key until it is exported once more.
func TestStaleIdentityBackupMessage(t *testing.T) {
	ctx := context.Background()
	db := newTestDatabase(t, InitDB)
	ds := NewDataService(db, "test")
	if err := ds.InitServer(ctx, false, "https://test.example", "https://test.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.InitServerKey(ctx, newCryptoService(), "identity-backup-test-passphrase"); err != nil {
		t.Fatal(err)
	}

	stale := func(want bool) {
		t.Helper()
		msg, err := staleIdentityBackupMessage(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if (msg != "") != want {
			t.Fatalf("stale = %q, want stale %v", msg, want)
		}
	}

	stale(true)
	if err := setIdentityBackupAt(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	stale(false)
	if _, err := db.Exec(`UPDATE private_keys SET created_at = created_at + interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	stale(true)
}

// first_boot_at is set when the database is initialized and never moves.
func TestFirstBootAtIsSetOnce(t *testing.T) {
	db := newTestDatabase(t, InitDB)
	if err := NewDataService(db, "test").InitServer(context.Background(), false, "https://test.example", "https://test.example"); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	if err := db.QueryRow(`SELECT first_boot_at FROM server_self`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(db); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	var rows int
	if err := db.QueryRow(`SELECT first_boot_at, (SELECT COUNT(*) FROM server_self) FROM server_self`).Scan(&again, &rows); err != nil {
		t.Fatal(err)
	}
	if !again.Equal(first) || rows != 1 {
		t.Fatalf("first_boot_at %v -> %v, %d rows", first, again, rows)
	}
}

// A server restored from its identity bundle starts: the bundle is its backup.
func TestImportedIdentityCountsAsBackedUp(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	b := restoreIdentity(t, a)
	msg, err := staleIdentityBackupMessage(context.Background(), b.db)
	if err != nil || msg != "" {
		t.Fatalf("restored server stale = %q (%v)", msg, err)
	}
}
