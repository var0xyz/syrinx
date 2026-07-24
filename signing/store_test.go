package signing

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOr("DB_HOST", "localhost"),
		envOr("DB_PORT", "5432"),
		envOr("DB_USER", "syrinx"),
		envOr("DB_PASSWORD", "syrinx"),
		envOr("DB_NAME", "syrinx"),
		envOr("DB_SSLMODE", "disable"),
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("ping db: %v", err)
	}
	if err := ensureSignatureTables(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureSignatureTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
			signed_fields  TEXT[] NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id             BIGSERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			signed_at      TIMESTAMP NOT NULL,
			algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
			signed_fields  TEXT[] NOT NULL DEFAULT '{}'
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func TestUserSignatureInsertGetWire(t *testing.T) {
	db := openStoreTestDB(t)
	suffix := time.Now().UnixNano()
	fp := fmt.Sprintf("usig-store-%d", suffix)
	fields := []string{"username", "fingerprint", "avatarURL"}

	id, err := InsertUserSignature(db, fp, "user-sig-b64", fields)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_signatures WHERE id = $1`, id)
	})

	row, err := GetUserSignature(db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Fingerprint != fp || row.Signature != "user-sig-b64" {
		t.Fatalf("row: %+v", row)
	}
	if row.Algorithm != defaultAlgorithm {
		t.Fatalf("algorithm: %q", row.Algorithm)
	}
	if len(row.SignedFields) != 3 || row.SignedFields[0] != "username" {
		t.Fatalf("signed_fields: %#v", row.SignedFields)
	}

	wire := UserWire(row)
	if wire.Signature != "user-sig-b64" || wire.SignatureFingerprint != fp {
		t.Fatalf("wire: %+v", wire)
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sf, ok := m["signedFields"].([]any)
	if !ok || len(sf) != 3 {
		t.Fatalf("json signedFields: %s", raw)
	}
}

func TestServerSignatureInsertGetWire(t *testing.T) {
	db := openStoreTestDB(t)
	suffix := time.Now().UnixNano()
	fp := fmt.Sprintf("ssig-store-%d", suffix)
	fields := []string{"userID", "fingerprint"}
	// Sub-second noise must be truncated on write.
	signedAt := time.Date(2026, 7, 24, 15, 30, 45, 123456789, time.UTC)

	id, err := InsertServerSignature(db, fp, "server-sig-b64", signedAt, fields)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM server_signatures WHERE id = $1`, id)
	})

	row, err := GetServerSignature(db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wantAt := signedAt.UTC().Truncate(time.Second)
	if !row.SignedAt.Equal(wantAt) {
		t.Fatalf("signed_at: got %v want %v", row.SignedAt, wantAt)
	}
	if row.Algorithm != defaultAlgorithm {
		t.Fatalf("algorithm: %q", row.Algorithm)
	}
	if len(row.SignedFields) != 2 {
		t.Fatalf("signed_fields: %#v", row.SignedFields)
	}

	wire := ServerWire(row, "srv1")
	if wire.ID != "srv1" || wire.Fingerprint != fp || wire.Signature != "server-sig-b64" {
		t.Fatalf("wire: %+v", wire)
	}
	if wire.Algorithm != defaultAlgorithm {
		t.Fatalf("wire algorithm: %q", wire.Algorithm)
	}
	if !wire.Timestamp.Equal(wantAt) {
		t.Fatalf("wire timestamp: %v", wire.Timestamp)
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sf, ok := m["signedFields"].([]any)
	if !ok || len(sf) != 2 {
		t.Fatalf("json signedFields: %s", raw)
	}
}

func TestInsertNilSignedFields(t *testing.T) {
	db := openStoreTestDB(t)
	suffix := time.Now().UnixNano()

	uid, err := InsertUserSignature(db, fmt.Sprintf("nil-u-%d", suffix), "sig", nil)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_signatures WHERE id = $1`, uid)
	})
	urow, err := GetUserSignature(db, uid)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if urow.SignedFields == nil || len(urow.SignedFields) != 0 {
		t.Fatalf("user signed_fields: %#v", urow.SignedFields)
	}

	sid, err := InsertServerSignature(db, fmt.Sprintf("nil-s-%d", suffix), "sig", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("insert server: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM server_signatures WHERE id = $1`, sid)
	})
	srow, err := GetServerSignature(db, sid)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if srow.SignedFields == nil || len(srow.SignedFields) != 0 {
		t.Fatalf("server signed_fields: %#v", srow.SignedFields)
	}

	uJSON, err := json.Marshal(UserWire(urow))
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	sJSON, err := json.Marshal(ServerWire(srow, "s"))
	if err != nil {
		t.Fatalf("marshal server: %v", err)
	}
	if !strings.Contains(string(uJSON), `"signedFields":[]`) {
		t.Fatalf("user wire want empty signedFields array: %s", uJSON)
	}
	if !strings.Contains(string(sJSON), `"signedFields":[]`) {
		t.Fatalf("server wire want empty signedFields array: %s", sJSON)
	}
}
