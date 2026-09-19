//go:build !ops

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openRecoveryBundleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, InitDB)
}

// TestRecoveryBundleRoundTrip exercises exportFromDB -> importIntoDB against
// real Postgres DBs — regression test for double-canonicalizing an already-
// canonical bundle id.
func TestRecoveryBundleRoundTrip(t *testing.T) {
	sourceDB := openRecoveryBundleTestDB(t)
	ds := NewDataService(sourceDB, "test")
	ds.setServerIDForTest("Ab3xY9pQ")
	if _, err := sourceDB.Exec(`INSERT INTO servers (id, name, self) VALUES ('Ab3xY9pQ', 'syrinx.example', TRUE)`); err != nil {
		t.Fatalf("seed self server: %v", err)
	}

	cryptoSvc := newCryptoService()
	passphrase := "recovery-bundle-test-pass-16"
	key, err := ds.InitServerKey(context.Background(), cryptoSvc, passphrase)
	if err != nil {
		t.Fatalf("InitServerKey: %v", err)
	}

	exportedAt := time.Now().UTC().Truncate(time.Second)
	bundle, err := exportFromDB(context.Background(), sourceDB, exportedAt)
	if err != nil {
		t.Fatalf("exportFromDB: %v", err)
	}
	if err := validateBundleDecrypt(bundle, cryptoSvc, passphrase); err != nil {
		t.Fatalf("validateBundleDecrypt: %v", err)
	}

	targetDB := openRecoveryBundleTestDB(t)
	result, err := importIntoDB(context.Background(), targetDB, cryptoSvc, passphrase, bundle)
	if err != nil {
		t.Fatalf("importIntoDB: %v", err)
	}
	if result != recoveryImportApplied {
		t.Fatalf("importIntoDB result = %v, want recoveryImportApplied", result)
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

	// Re-importing the identical bundle must report recoveryImportAlreadyPresent,
	// not a mismatch — this is the exact check that was broken.
	result2, err := importIntoDB(context.Background(), sourceDB, cryptoSvc, passphrase, bundle)
	if err != nil {
		t.Fatalf("importIntoDB (already present): %v", err)
	}
	if result2 != recoveryImportAlreadyPresent {
		t.Fatalf("importIntoDB result = %v, want recoveryImportAlreadyPresent", result2)
	}
}
