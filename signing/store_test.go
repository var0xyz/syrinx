package signing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
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
		envOr("DB_NAME", "syrinx_test"),
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
			public_key_id  VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id             SERIAL PRIMARY KEY,
			fingerprint    VARCHAR(255) NOT NULL,
			signature      TEXT NOT NULL,
			signed_at      TIMESTAMP NOT NULL
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

	id, err := InsertUserSignature(context.Background(), db, fp, "user-sig-b64")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_signatures WHERE id = $1`, id)
	})

	row, err := GetUserSignature(context.Background(), db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.PublicKeyID != fp || row.Signature != "user-sig-b64" {
		t.Fatalf("row: %+v", row)
	}

	wire := UserWire(row)
	if wire.Armor != "user-sig-b64" || wire.Fingerprint != fp {
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
	if _, ok := m["armor"]; !ok {
		t.Fatalf("json missing armor: %s", raw)
	}
	if _, ok := m["fingerprint"]; !ok {
		t.Fatalf("json missing fingerprint: %s", raw)
	}
	if _, ok := m["algorithm"]; ok {
		t.Fatalf("json must not include algorithm: %s", raw)
	}
	if _, ok := m["fields"]; ok {
		t.Fatalf("json must not include fields: %s", raw)
	}
}

func TestServerSignatureInsertGetWire(t *testing.T) {
	db := openStoreTestDB(t)
	suffix := time.Now().UnixNano()
	fp := fmt.Sprintf("ssig-store-%d", suffix)
	// Sub-second noise must be truncated on write.
	signedAt := time.Date(2026, 7, 24, 15, 30, 45, 123456789, time.UTC)

	id, err := InsertServerSignature(context.Background(), db, fp, "server-sig-b64", signedAt)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM server_signatures WHERE id = $1`, id)
	})

	row, err := GetServerSignature(context.Background(), db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wantAt := signedAt.UTC().Truncate(time.Second)
	if !row.SignedAt.Equal(wantAt) {
		t.Fatalf("signed_at: got %v want %v", row.SignedAt, wantAt)
	}

	wire := ServerWire(row, "srv1")
	if wire.ServerID != "srv1" || wire.Fingerprint != fp || wire.Armor != "server-sig-b64" {
		t.Fatalf("wire: %+v", wire)
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
	if _, ok := m["serverID"]; !ok {
		t.Fatalf("json missing serverID: %s", raw)
	}
	if _, ok := m["algorithm"]; ok {
		t.Fatalf("json must not include algorithm: %s", raw)
	}
	if _, ok := m["fields"]; ok {
		t.Fatalf("json must not include fields: %s", raw)
	}
}
