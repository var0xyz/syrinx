//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func ensureThreadStoreSchema(db *sql.DB) error {
	if err := ensureMentionsSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TABLE reed_threads (
		id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	)`)
	return err
}

// newThreadParts returns n parts for author with ascending reed IDs.
func newThreadParts(t *testing.T, author string, n int) []createReedParams {
	t.Helper()
	parts := make([]createReedParams, n)
	for i := range parts {
		parts[i] = createReedParams{
			ReedID: author + "/" + newTestReedID(t), UserID: author, UserKeyID: "alicefp",
			UserSignature: "sig", ServerFingerprint: "srvfp", ServerSignature: "sig",
		}
	}
	return parts
}

func newThreadParams(author, previousID string, parts []createReedParams) createThreadParams {
	return createThreadParams{
		UserID: author, UserKeyID: "alicefp", UserSignature: "threadsig",
		ServerFingerprint: "srvfp", ServerSignature: "threadsrvsig",
		Timestamp: time.Now().UTC(), PreviousID: previousID, Parts: parts,
	}
}

func TestCreateThread_StoresPartsAndRecord(t *testing.T) {
	db := newTestDatabase(t, ensureThreadStoreSchema)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}
	seedMentionUser(t, db, "alice")
	author := "alice@testserver"

	parts := newThreadParts(t, author, 3)
	if _, err := svc.CreateThread(ctx, newThreadParams(author, "", parts)); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	rec, err := svc.GetThreadRecord(ctx, parts[0].ReedID)
	if err != nil || rec == nil {
		t.Fatalf("GetThreadRecord: %v, %v", rec, err)
	}
	if rec.UserID != author || rec.UserSignature != "threadsig" || rec.ServerSignature != "threadsrvsig" {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if len(rec.ReedIDs) != 3 {
		t.Fatalf("got %d parts, want 3", len(rec.ReedIDs))
	}
	for i, id := range rec.ReedIDs {
		if id != parts[i].ReedID {
			t.Fatalf("part %d = %s, want %s", i, id, parts[i].ReedID)
		}
	}

	// The last part is the author's tip: a reed naming it succeeds.
	next := newThreadParts(t, author, 1)[0]
	next.PreviousID = parts[2].ReedID
	next.Timestamp = time.Now().UTC()
	if _, err := svc.CreateReed(ctx, next); err != nil {
		t.Fatalf("reed after thread naming its last part: %v", err)
	}
}

func TestCreateThread_StaleTipForks(t *testing.T) {
	db := newTestDatabase(t, ensureThreadStoreSchema)
	ctx := context.Background()
	svc := &DataService{db: db, serverID: "testserver"}
	seedMentionUser(t, db, "alice")
	author := "alice@testserver"

	_, err := svc.CreateThread(ctx, newThreadParams(author, "not-the-tip", newThreadParts(t, author, 2)))
	if !errors.Is(err, ErrReedFork) {
		t.Fatalf("expected ErrReedFork, got %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reeds`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected no reeds after a failed thread, got %d (%v)", n, err)
	}
}

func TestCreateThread_MissingRecordIsNil(t *testing.T) {
	db := newTestDatabase(t, ensureThreadStoreSchema)
	svc := &DataService{db: db, serverID: "testserver"}
	rec, err := svc.GetThreadRecord(context.Background(), "alice@testserver/none")
	if err != nil || rec != nil {
		t.Fatalf("expected nil record, got %+v, %v", rec, err)
	}
}

func TestValidateThreadParts(t *testing.T) {
	author := "alice@testserver"
	reversed := newThreadParts(t, author, 2)
	reversed[0], reversed[1] = reversed[1], reversed[0]
	foreign := newThreadParts(t, author, 2)
	foreign[1].ReedID = "bob@testserver/" + newTestReedID(t)

	tests := []struct {
		name  string
		parts []createReedParams
		ok    bool
	}{
		{"two parts", newThreadParts(t, author, 2), true},
		{"thirty parts", newThreadParts(t, author, MaxThreadReeds), true},
		{"one part", newThreadParts(t, author, 1), false},
		{"thirty-one parts", newThreadParts(t, author, MaxThreadReeds+1), false},
		{"descending IDs", reversed, false},
		{"another author's reed", foreign, false},
	}
	for _, tc := range tests {
		err := validateThreadParts(author, tc.parts)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidThread) {
			t.Errorf("%s: expected ErrInvalidThread, got %v", tc.name, err)
		}
	}
}
