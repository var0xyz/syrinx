//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

const vouchTestServerID = "testserver"

func openVouchTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureVouchSchema)
}

func ensureVouchSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (id SERIAL PRIMARY KEY, public_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (id SERIAL PRIMARY KEY, private_key_id VARCHAR(255) NOT NULL, signature TEXT NOT NULL, signed_at TIMESTAMP NOT NULL)`,
		`DROP TABLE IF EXISTS user_vouches_active CASCADE`,
		`DROP TABLE IF EXISTS user_vouches CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE user_vouches (
			id VARCHAR(255) PRIMARY KEY,
			voucher_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			voucher_key_id VARCHAR(255) NOT NULL,
			subject_user_id VARCHAR(255) NOT NULL,
			subject_key_id VARCHAR(255) NOT NULL,
			note VARCHAR(140) NOT NULL DEFAULT '',
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			withdrawal_signature_id INT REFERENCES user_signatures(id),
			withdrawal_server_signature_id INT REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE user_vouches_active (
			vouch_id VARCHAR(255) PRIMARY KEY REFERENCES user_vouches(id) ON DELETE CASCADE,
			voucher_user_id VARCHAR(255) NOT NULL,
			subject_user_id VARCHAR(255) NOT NULL,
			subject_key_id VARCHAR(255) NOT NULL,
			UNIQUE (voucher_user_id, subject_key_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func insertVouchTestIdentity(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	id := string(canonicalID(vouchTestServerID, userID))
	if _, err := db.Exec(
		`INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		id, vouchTestServerID,
	); err != nil {
		t.Fatalf("insert identity %s: %v", userID, err)
	}
	return id
}

// testVouchID mints a canonical vouch id owned by voucherID, since the
// store rejects anything that is not voucherID@serverID/UUIDv7.
func testVouchID(t *testing.T, voucherID string) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint uuidv7: %v", err)
	}
	return string(appendEntity(identityID(voucherID), u.String()))
}

// testVouch builds a cert for (voucher, subjectKey) with a distinct
// signature per call, so replay vs conflict is distinguishable.
func testVouch(voucherID, voucherKeyID, subjectUserID, subjectKeyID, note, sig, vouchID string, at time.Time) VouchCert {
	return VouchCert{
		ID:            vouchID,
		VoucherUserID: voucherID,
		VoucherKeyID:  voucherKeyID,
		SubjectUserID: subjectUserID,
		SubjectKeyID:  subjectKeyID,
		Note:          note,
		UserSignature: UserSignature{ID: voucherKeyID, Armor: sig},
		ServerSignature: ServerSignature{
			ID:       "server-key",
			Armor:    "server-sig",
			SignedAt: at,
		},
	}
}

func TestInsertVouchIdempotentReplay(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	bobKey := "bob@peer/k1"
	now := time.Now().UTC().Truncate(time.Second)

	cert := testVouch(alice, alice+"/ka", "bob@peer", bobKey, "met once", "SIG1", testVouchID(t, alice), now)
	if err := svc.InsertVouch(ctx, cert); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := svc.InsertVouch(ctx, cert); err != nil {
		t.Fatalf("replay should be idempotent: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM user_vouches`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row after replay, got %d", rows)
	}
}

func TestInsertVouchDifferentSignatureConflicts(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	now := time.Now().UTC().Truncate(time.Second)

	first := testVouch(alice, alice+"/ka", "bob@peer", "bob@peer/k1", "", "SIG1", testVouchID(t, alice), now)
	if err := svc.InsertVouch(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	second := testVouch(alice, alice+"/ka", "bob@peer", "bob@peer/k1", "", "SIG2", testVouchID(t, alice), now)
	if err := svc.InsertVouch(ctx, second); !errors.Is(err, ErrVouchConflict) {
		t.Errorf("expected ErrVouchConflict, got %v", err)
	}
}

// Vouching for a second key of the same subject is a new row, which is
// what preserves the earlier verification as evidence.
func TestInsertVouchSecondKeySameSubject(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	now := time.Now().UTC().Truncate(time.Second)

	for i, key := range []string{"bob@peer/k1", "bob@peer/k2"} {
		cert := testVouch(alice, alice+"/ka", "bob@peer", key, "", "SIG", testVouchID(t, alice), now.Add(time.Duration(i)*time.Second))
		if err := svc.InsertVouch(ctx, cert); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}

	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM user_vouches`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("expected 2 live rows for two subject keys, got %d", rows)
	}
}

func testWithdrawalServerSig(signedAt time.Time) ServerSignature {
	return ServerSignature{ID: vouchTestServerID + "/srv", Armor: "WSRV", SignedAt: signedAt}
}

func TestWithdrawVouchRetainsRow(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	subjectKey := "bob@peer/k1"
	now := time.Now().UTC().Truncate(time.Second)
	vouchID := testVouchID(t, alice)

	if err := svc.InsertVouch(ctx, testVouch(alice, alice+"/ka", "bob@peer", subjectKey, "", "SIG1", vouchID, now)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Withdrawal signed by a different key than the vouch: a voucher who
	// rotated must still be able to retract.
	cert, err := svc.WithdrawVouch(
		ctx, alice, subjectKey,
		UserSignature{ID: alice + "/kb", Armor: "WSIG"},
		testWithdrawalServerSig(now),
	)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if cert.Withdrawal == nil {
		t.Fatal("returned cert carries no withdrawal")
	}
	if cert.Withdrawal.UserSignature.Armor != "WSIG" {
		t.Error("withdrawal user signature missing from returned cert")
	}
	// Without the countersignature the retraction time is bound by nothing.
	if cert.Withdrawal.ServerSignature.Armor != "WSRV" {
		t.Error("withdrawal countersignature missing from returned cert")
	}

	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM user_vouches`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("expected the withdrawn row to be retained, got %d", rows)
	}
	// Dropping the active row is what removes it from every list read.
	if err := db.QueryRow(`SELECT count(*) FROM user_vouches_active`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("withdrawn vouch still active, got %d rows", rows)
	}
}

func TestWithdrawVouchMissing(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	_, err := svc.WithdrawVouch(
		ctx, alice, "bob@peer/nope",
		UserSignature{ID: alice + "/ka", Armor: "WSIG"},
		testWithdrawalServerSig(time.Now().UTC()),
	)
	if !errors.Is(err, ErrVouchNotFound) {
		t.Errorf("expected ErrVouchNotFound, got %v", err)
	}
}

// Re-verifying after a retraction is legitimate, and it must not destroy
// the withdrawal: a peer mid-reconcile still needs to fetch and verify it.
func TestReVouchAfterWithdrawalAddsRow(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	subjectKey := "bob@peer/k1"
	now := time.Now().UTC().Truncate(time.Second)
	firstID := testVouchID(t, alice)

	if err := svc.InsertVouch(ctx, testVouch(alice, alice+"/ka", "bob@peer", subjectKey, "", "SIG1", firstID, now)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := svc.WithdrawVouch(
		ctx, alice, subjectKey,
		UserSignature{ID: alice + "/ka", Armor: "WSIG"},
		testWithdrawalServerSig(now),
	); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	secondID := testVouchID(t, alice)
	again := testVouch(alice, alice+"/kb", "bob@peer", subjectKey, "met again", "SIG2", secondID, now.Add(time.Minute))
	if err := svc.InsertVouch(ctx, again); err != nil {
		t.Fatalf("re-vouch: %v", err)
	}

	got, err := svc.GetVouch(ctx, alice, subjectKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != secondID {
		t.Errorf("live vouch should be the new row: got %q want %q", got.ID, secondID)
	}
	if got.Withdrawal != nil {
		t.Error("the new vouch is marked withdrawn")
	}
	if got.UserSignature.Armor != "SIG2" || got.VoucherKeyID != alice+"/kb" {
		t.Errorf("re-vouch did not install the new signature: %+v", got.UserSignature)
	}
	if got.Note != "met again" {
		t.Errorf("note not stored on re-vouch: %q", got.Note)
	}

	// The withdrawn original survives with its retraction intact.
	old, err := svc.GetVouchByID(ctx, firstID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if old == nil || old.Withdrawal == nil {
		t.Fatal("the withdrawn original lost its withdrawal")
	}
	if old.Withdrawal.ServerSignature.Armor != "WSRV" {
		t.Error("withdrawal countersignature lost on re-vouch")
	}
}

// A vouch may name a subject on a peer server, who has no local identity.
func TestInsertVouchForeignSubject(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	cert := testVouch(
		alice, alice+"/ka", "stranger@elsewhere", "stranger@elsewhere/k9", "",
		"SIG1", testVouchID(t, alice), time.Now().UTC().Truncate(time.Second),
	)
	if err := svc.InsertVouch(ctx, cert); err != nil {
		t.Fatalf("foreign subject should insert without a local identity: %v", err)
	}
}

// The id is what a client reconciles against, so a malformed one must be
// refused at the store rather than trusted from whatever minted it.
func TestInsertVouchRejectsMalformedID(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	now := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name    string
		vouchID string
	}{
		{"empty", ""},
		{"no uuid part", alice},
		{"uuid only", "0193a1f0-1111-7000-8000-000000000000"},
		{"not a uuid", alice + "/not-a-uuid"},
		{"uuidv4 not v7", alice + "/6f8c1b3e-4a2d-4c7f-9b1e-2d3a4b5c6d7e"},
		{"owned by another user", string(canonicalID(vouchTestServerID, "mallory")) + "/0193a1f0-1111-7000-8000-000000000000"},
		{"foreign server", "alice@elsewhere/0193a1f0-1111-7000-8000-000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := testVouch(alice, alice+"/ka", "bob@peer", "bob@peer/k1", "", "SIG1", tc.vouchID, now)
			if err := svc.InsertVouch(ctx, cert); !errors.Is(err, ErrVouchInvalidID) {
				t.Errorf("expected ErrVouchInvalidID, got %v", err)
			}
		})
	}
}

func TestInsertVouchAcceptsCanonicalID(t *testing.T) {
	db := openVouchTestDB(t)
	svc := &DataService{db: db, serverID: vouchTestServerID}
	ctx := context.Background()

	alice := insertVouchTestIdentity(t, db, "alice")
	id := testVouchID(t, alice)
	cert := testVouch(
		alice, alice+"/ka", "bob@peer", "bob@peer/k1", "", "SIG1", id,
		time.Now().UTC().Truncate(time.Second),
	)
	if err := svc.InsertVouch(ctx, cert); err != nil {
		t.Fatalf("canonical id rejected: %v", err)
	}
	got, err := svc.GetVouch(ctx, alice, "bob@peer/k1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Errorf("stored id %q, want %q", got.ID, id)
	}
}

// isVouchIDWellFormed gates the read path, where the owner is not known up
// front, so it checks shape only.
func TestIsVouchIDWellFormed(t *testing.T) {
	good := "alice@home/0193a1f0-1111-7000-8000-000000000000"
	if !isVouchIDWellFormed(good) {
		t.Errorf("rejected a canonical id: %q", good)
	}
	for _, bad := range []string{
		"",
		"alice@home",
		"alice@home/nope",
		"0193a1f0-1111-7000-8000-000000000000",
		"alice@home/6f8c1b3e-4a2d-4c7f-9b1e-2d3a4b5c6d7e",
	} {
		if isVouchIDWellFormed(bad) {
			t.Errorf("accepted a malformed id: %q", bad)
		}
	}
}
