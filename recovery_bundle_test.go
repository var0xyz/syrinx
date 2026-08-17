//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/recovery"

	_ "github.com/lib/pq"
)

func openRecoveryBundleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, InitDB)
}

// TestRecoveryBundleRoundTrip exercises ExportFromDB -> ImportIntoDB against
// real Postgres DBs — regression test for double-canonicalizing an already-
// canonical bundle id.
func TestRecoveryBundleRoundTrip(t *testing.T) {
	sourceDB := openRecoveryBundleTestDB(t)
	ds := NewDataService(sourceDB, "test")
	ds.setServerIDForTest("Ab3xY9pQ")
	if _, err := sourceDB.Exec(`INSERT INTO servers (id, name, self) VALUES ('Ab3xY9pQ', 'syrinx.example', TRUE)`); err != nil {
		t.Fatalf("seed self server: %v", err)
	}

	cryptoSvc := crypto.NewService()
	passphrase := "recovery-bundle-test-pass-16"
	key, err := ds.InitServerKey(context.Background(), cryptoSvc, passphrase)
	if err != nil {
		t.Fatalf("InitServerKey: %v", err)
	}

	exportedAt := time.Now().UTC().Truncate(time.Second)
	bundle, err := recovery.ExportFromDB(context.Background(), sourceDB, exportedAt)
	if err != nil {
		t.Fatalf("ExportFromDB: %v", err)
	}
	if err := recovery.ValidateDecrypt(bundle, cryptoSvc, passphrase); err != nil {
		t.Fatalf("ValidateDecrypt: %v", err)
	}

	targetDB := openRecoveryBundleTestDB(t)
	result, err := recovery.ImportIntoDB(context.Background(), targetDB, cryptoSvc, passphrase, bundle)
	if err != nil {
		t.Fatalf("ImportIntoDB: %v", err)
	}
	if result != recovery.ImportApplied {
		t.Fatalf("ImportIntoDB result = %v, want ImportApplied", result)
	}

	targetDS := NewDataService(targetDB, "test")
	targetDS.setServerIDForTest("Ab3xY9pQ")
	restoredKey, err := targetDS.InitServerKey(context.Background(), cryptoSvc, passphrase)
	if err != nil {
		t.Fatalf("InitServerKey on restored DB: %v", err)
	}
	if restoredKey.Fingerprint != key.Fingerprint {
		t.Fatalf("restored fingerprint = %s, want %s", restoredKey.Fingerprint, key.Fingerprint)
	}

	// Re-importing the identical bundle must report ImportAlreadyPresent,
	// not a mismatch — this is the exact check that was broken.
	result2, err := recovery.ImportIntoDB(context.Background(), sourceDB, cryptoSvc, passphrase, bundle)
	if err != nil {
		t.Fatalf("ImportIntoDB (already present): %v", err)
	}
	if result2 != recovery.ImportAlreadyPresent {
		t.Fatalf("ImportIntoDB result = %v, want ImportAlreadyPresent", result2)
	}
}
