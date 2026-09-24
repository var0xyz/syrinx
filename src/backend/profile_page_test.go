//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/lib/pq"
)

const profilePageTestServerID = "testserver"

func openProfilePageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureProfilePageSchema)
}

func ensureProfilePageSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS reed_server_allocations CASCADE`,
		`DROP TABLE IF EXISTS reed_allocations CASCADE`,
		`DROP TABLE IF EXISTS reed_removals CASCADE`,
		`DROP TABLE IF EXISTS reeds CASCADE`,
		`DROP TABLE IF EXISTS reed_identities CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE reeds (
			id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			signed_at TIMESTAMP NOT NULL,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE reed_removals (
			reed_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
			PRIMARY KEY (user_id, reed_id)
		)`,
		`CREATE TABLE reed_allocations (
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			holder_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			PRIMARY KEY (reed_id, holder_user_id)
		)`,
		`CREATE TABLE reed_server_allocations (
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			server_id VARCHAR(16) NOT NULL,
			PRIMARY KEY (reed_id, server_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func insertProfilePageIdentity(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	identity := string(canonicalID(profilePageTestServerID, userID))
	if _, err := db.Exec(`INSERT INTO identities (id, server_id) VALUES ($1, $2)`, identity, profilePageTestServerID); err != nil {
		t.Fatalf("insert identity %s: %v", userID, err)
	}
	return identity
}

// seedProfilePageReeds inserts n reeds for author, ids ordered so that a
// higher index is a newer reed — matching uuidv7's own ordering.
func seedProfilePageReeds(t *testing.T, db *sql.DB, author string, n int) []string {
	t.Helper()
	var userSigID, serverSigID int
	if err := db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ('pk', 'sig') RETURNING id`).Scan(&userSigID); err != nil {
		t.Fatalf("insert user_signatures: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('pk', 'sig', now()) RETURNING id`).Scan(&serverSigID); err != nil {
		t.Fatalf("insert server_signatures: %v", err)
	}

	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		reedID := fmt.Sprintf("%s/%04d", author, i)
		if _, err := db.Exec(`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`, reedID, profilePageTestServerID); err != nil {
			t.Fatalf("insert reed_identities %s: %v", reedID, err)
		}
		if _, err := db.Exec(
			`INSERT INTO reeds (id, user_id, signed_at, user_signature_id, server_signature_id)
			 VALUES ($1, $2, now(), $3, $4)`,
			reedID, author, userSigID, serverSigID,
		); err != nil {
			t.Fatalf("insert reed %s: %v", reedID, err)
		}
		ids = append(ids, reedID)
	}
	return ids
}

func TestGetAuthorReedPage_OrdersNewestFirstAcrossPages(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	ids := seedProfilePageReeds(t, db, author, 120)

	page1, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 1, 50)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 50 || !hasMore {
		t.Fatalf("page 1 = %d reeds, hasMore=%v; want 50, true", len(page1), hasMore)
	}
	// Newest first: the last-seeded id must lead page 1.
	if page1[0] != ids[119] {
		t.Fatalf("page 1 head = %s, want %s (newest first)", page1[0], ids[119])
	}

	page2, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 2, 50)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 50 || !hasMore {
		t.Fatalf("page 2 = %d reeds, hasMore=%v; want 50, true", len(page2), hasMore)
	}
	if page2[0] != ids[69] {
		t.Fatalf("page 2 head = %s, want %s (continues page 1)", page2[0], ids[69])
	}

	page3, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 3, 50)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3) != 20 || hasMore {
		t.Fatalf("page 3 = %d reeds, hasMore=%v; want 20, false (last page)", len(page3), hasMore)
	}
}

func TestGetAuthorReedPage_PastEndIsEmpty(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	seedProfilePageReeds(t, db, author, 10)

	ids, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 5, 50)
	if err != nil {
		t.Fatalf("page 5: %v", err)
	}
	if len(ids) != 0 || hasMore {
		t.Fatalf("page past end = %d reeds, hasMore=%v; want 0, false", len(ids), hasMore)
	}
}

func TestGetAuthorReedPage_ExcludesRemovedReeds(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	ids := seedProfilePageReeds(t, db, author, 10)

	if _, err := db.Exec(`INSERT INTO reed_removals (reed_id, user_id) VALUES ($1, $2)`, ids[9], author); err != nil {
		t.Fatalf("insert removal: %v", err)
	}

	page, _, err := s.GetAuthorReedPage(context.Background(), author, 1, 50)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page) != 9 {
		t.Fatalf("page = %d reeds, want 9 (removed one excluded)", len(page))
	}
	for _, id := range page {
		if id == ids[9] {
			t.Fatal("removed reed present in page")
		}
	}
}

// The page describes the author, so two viewers holding different subsets
// must see identical results — that is what makes PAGE_ACK leak-free.
func TestGetAuthorReedPage_IsViewerIndependent(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	viewer := insertProfilePageIdentity(t, db, "bob")
	ids := seedProfilePageReeds(t, db, author, 60)

	for _, id := range ids[:30] {
		if _, err := db.Exec(`INSERT INTO reed_allocations (reed_id, holder_user_id) VALUES ($1, $2)`, id, viewer); err != nil {
			t.Fatalf("insert allocation: %v", err)
		}
	}

	page, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 1, 50)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page) != 50 || !hasMore {
		t.Fatalf("page = %d reeds, hasMore=%v; want 50, true regardless of holdings", len(page), hasMore)
	}
}

func TestSubtractHeldReeds_DropsHeldAndKeepsOrder(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	viewer := insertProfilePageIdentity(t, db, "bob")
	ids := seedProfilePageReeds(t, db, author, 6)

	// Hold every other reed.
	for i := 0; i < len(ids); i += 2 {
		if _, err := db.Exec(`INSERT INTO reed_allocations (reed_id, holder_user_id) VALUES ($1, $2)`, ids[i], viewer); err != nil {
			t.Fatalf("insert allocation: %v", err)
		}
	}

	ordered := []string{ids[5], ids[4], ids[3], ids[2], ids[1], ids[0]}
	missing, err := s.SubtractHeldReeds(context.Background(), ordered, viewer)
	if err != nil {
		t.Fatalf("subtract: %v", err)
	}

	want := []string{ids[5], ids[3], ids[1]}
	if len(missing) != len(want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
	for i := range want {
		if missing[i] != want[i] {
			t.Fatalf("missing = %v, want %v (input order must survive)", missing, want)
		}
	}
}

func TestSubtractHeldReeds_EmptyInput(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	viewer := insertProfilePageIdentity(t, db, "bob")

	missing, err := s.SubtractHeldReeds(context.Background(), nil, viewer)
	if err != nil {
		t.Fatalf("subtract: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want empty", missing)
	}
}

// reed_server_allocations is per-server, so a peer already holding the
// whole page gets nothing back while the author demonstrably has more —
// which is why hasMore must travel separately from the event list.
func TestSubtractServerAllocatedReeds_ShortResultDoesNotMeanEndOfList(t *testing.T) {
	db := openProfilePageTestDB(t)
	s := &DataService{db: db, serverID: profilePageTestServerID}
	author := insertProfilePageIdentity(t, db, "alice")
	seedProfilePageReeds(t, db, author, 60)

	page, hasMore, err := s.GetAuthorReedPage(context.Background(), author, 1, 50)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	for _, id := range page {
		if _, err := db.Exec(`INSERT INTO reed_server_allocations (reed_id, server_id) VALUES ($1, $2)`, id, "peer5678"); err != nil {
			t.Fatalf("insert server allocation: %v", err)
		}
	}

	missing, err := s.SubtractServerAllocatedReeds(context.Background(), page, "peer5678")
	if err != nil {
		t.Fatalf("subtract: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %d, want 0 (peer holds the whole page)", len(missing))
	}
	if !hasMore {
		t.Fatal("hasMore = false; the author has 60 reeds, so more remain past page 1")
	}
}
