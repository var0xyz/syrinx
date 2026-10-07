//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

const serverKeyTestPassphrase = "server-key-test-passphrase-16"

// newServerKeyTestDB returns a fresh schema with a self server row and its
// first signing key, minted the way a first boot does.
func newServerKeyTestDB(t *testing.T) (*sql.DB, *DataService, *ServerSigningKey) {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	ds := NewDataService(db, "test")
	ds.setServerIDForTest("Ab3xY9pQ")
	if _, err := db.Exec(`INSERT INTO servers (id, name, self) VALUES ('Ab3xY9pQ', 'syrinx.example', TRUE)`); err != nil {
		t.Fatalf("seed self server: %v", err)
	}
	key, err := ds.InitServerKey(context.Background(), newCryptoService(), serverKeyTestPassphrase)
	if err != nil {
		t.Fatalf("first boot InitServerKey: %v", err)
	}
	return db, ds, key
}

func publicArmorOf(t *testing.T, db *sql.DB, keyID string) string {
	t.Helper()
	var armor string
	if err := db.QueryRow(`SELECT armor FROM public_keys WHERE id = $1`, keyID).Scan(&armor); err != nil {
		t.Fatalf("load public key %s: %v", keyID, err)
	}
	return armor
}

func TestServerKeyRevocationPayloadCanonicalShape(t *testing.T) {
	got := buildServerKeyRevocationPayload(
		"home", "old@home", "new@home", true, "laptop stolen",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	want := "---\n" +
		"compromised: true\n" +
		"keyID: old@home\n" +
		"serverID: home\n" +
		"signedAt: 2026-10-07T12:00:00Z\n" +
		"successor: new@home\n" +
		"type: server-key-revocation\n" +
		"---\n" +
		"laptop stolen"
	if string(got) != want {
		t.Errorf("compromised revocation payload mismatch:\n got=%q\nwant=%q", got, want)
	}

	// A planned rotation: not compromised, no reason, so no content.
	got = buildServerKeyRevocationPayload(
		"home", "old@home", "new@home", false, "",
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	want = "---\n" +
		"compromised: false\n" +
		"keyID: old@home\n" +
		"serverID: home\n" +
		"signedAt: 2026-10-07T12:00:00Z\n" +
		"successor: new@home\n" +
		"type: server-key-revocation\n" +
		"---\n"
	if string(got) != want {
		t.Errorf("rotation payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestInitServerKeyLoadsExistingKey(t *testing.T) {
	_, ds, first := newServerKeyTestDB(t)
	again, err := ds.InitServerKey(context.Background(), newCryptoService(), serverKeyTestPassphrase)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if again.Fingerprint != first.Fingerprint {
		t.Fatalf("second boot loaded %s, want %s", again.Fingerprint, first.Fingerprint)
	}
}

// revocationRow reads a revoked key's state and its revocation row.
func revocationRow(t *testing.T, db *sql.DB, keyID string) (revoked bool, compromised bool, reason string) {
	t.Helper()
	var revokedAt sql.NullTime
	if err := db.QueryRow(`SELECT revoked_at FROM private_keys WHERE id = $1`, keyID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT compromised, reason FROM private_key_revocations WHERE key_id = $1`, keyID).
		Scan(&compromised, &reason); err != nil {
		t.Fatal(err)
	}
	return revokedAt.Valid, compromised, reason
}

func TestRotateServerKey(t *testing.T) {
	db, ds, first := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()

	rev, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	booted, err := ds.InitServerKey(ctx, cryptoSvc, serverKeyTestPassphrase)
	if err != nil {
		t.Fatalf("boot after rotation: %v", err)
	}
	if booted.Fingerprint == first.Fingerprint {
		t.Fatal("boot after rotation still signs with the old key")
	}
	if want := string(canonicalID("Ab3xY9pQ", booted.Fingerprint)); rev.Successor != want {
		t.Fatalf("revocation names %s as successor, booted key is %s", rev.Successor, want)
	}

	revoked, compromised, _ := revocationRow(t, db, rev.KeyID)
	if !revoked || compromised {
		t.Fatalf("old key revoked=%v compromised=%v, want revoked and not compromised", revoked, compromised)
	}
	var predecessor string
	if err := db.QueryRow(`SELECT predecessor_id FROM public_keys WHERE id = $1`, rev.Successor).Scan(&predecessor); err != nil {
		t.Fatal(err)
	}
	if predecessor != rev.KeyID {
		t.Fatalf("successor's predecessor = %s, want %s", predecessor, rev.KeyID)
	}

	// Each signature is stored under the key that made it.
	var sigKey, succKey string
	if err := db.QueryRow(`
		SELECT sig.private_key_id, succ.private_key_id
		FROM private_key_revocations r
		JOIN server_signatures sig ON sig.id = r.server_signature_id
		JOIN server_signatures succ ON succ.id = r.successor_signature_id
		WHERE r.key_id = $1
	`, rev.KeyID).Scan(&sigKey, &succKey); err != nil {
		t.Fatal(err)
	}
	if sigKey != rev.KeyID || succKey != rev.Successor {
		t.Fatalf("signatures stored under %s and %s", sigKey, succKey)
	}

	payload := string(buildServerKeyRevocationPayload("Ab3xY9pQ", rev.KeyID, rev.Successor, rev.Compromised, rev.Reason, rev.SignedAt))
	if err := cryptoSvc.verifySignature(payload, rev.Signature, publicArmorOf(t, db, rev.KeyID)); err != nil {
		t.Errorf("revoked key's signature: %v", err)
	}
	if err := cryptoSvc.verifySignature(payload, rev.SuccessorSignature, publicArmorOf(t, db, rev.Successor)); err != nil {
		t.Errorf("successor's signature: %v", err)
	}
}

func TestRevokeCompromisedServerKey(t *testing.T) {
	db, _, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()

	if _, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, true, "  "); err == nil {
		t.Fatal("compromised revocation with a blank reason was accepted")
	}

	rev, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, true, "laptop stolen")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, compromised, reason := revocationRow(t, db, rev.KeyID)
	if !revoked || !compromised || reason != "laptop stolen" {
		t.Fatalf("old key revoked=%v compromised=%v reason=%q", revoked, compromised, reason)
	}
}

func TestServerKeyRefusesRevokedCurrentKey(t *testing.T) {
	db, ds, first := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()
	firstID := string(canonicalID("Ab3xY9pQ", first.Fingerprint))

	// A hand-edited DB: the current key is revoked but nothing replaced it.
	if _, err := db.Exec(`UPDATE private_keys SET revoked_at = NOW() WHERE id = $1`, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := revokeServerKey(ctx, db, cryptoSvc, serverKeyTestPassphrase, false, ""); !errors.Is(err, errServerKeyRevoked) {
		t.Fatalf("revoking a revoked key: got %v, want errServerKeyRevoked", err)
	}
	if _, err := ds.InitServerKey(ctx, cryptoSvc, serverKeyTestPassphrase); err == nil {
		t.Fatal("boot with a revoked current key succeeded")
	}

	// Keys exist but none is current: boot must not mint a fresh one.
	if _, err := db.Exec(`UPDATE servers SET signing_key = NULL WHERE self = TRUE`); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.InitServerKey(ctx, cryptoSvc, serverKeyTestPassphrase); err == nil {
		t.Fatal("boot with keys but no current key minted a new one")
	}
}

func TestRecoveryBundleCarriesKeyChain(t *testing.T) {
	sourceDB, _, _ := newServerKeyTestDB(t)
	ctx := context.Background()
	cryptoSvc := newCryptoService()

	rotated, err := revokeServerKey(ctx, sourceDB, cryptoSvc, serverKeyTestPassphrase, false, "")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	compromised, err := revokeServerKey(ctx, sourceDB, cryptoSvc, serverKeyTestPassphrase, true, "leaked")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}

	bundle, err := exportFromDB(ctx, sourceDB, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(bundle.Revocations) != 2 {
		t.Fatalf("bundle has %d revocations, want 2", len(bundle.Revocations))
	}

	targetDB := newTestDatabase(t, InitDB)
	if _, err := importIntoDB(ctx, targetDB, cryptoSvc, serverKeyTestPassphrase, bundle); err != nil {
		t.Fatalf("import: %v", err)
	}
	restored, err := loadServerKeyRevocations(ctx, targetDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || restored[0].KeyID != rotated.KeyID || restored[1].KeyID != compromised.KeyID {
		t.Fatalf("restored revocations = %+v", restored)
	}
	if restored[0].Compromised || !restored[1].Compromised || restored[1].Reason != "leaked" {
		t.Fatalf("restored compromised flags or reason wrong: %+v", restored)
	}

	var predecessor sql.NullString
	if err := targetDB.QueryRow(`SELECT predecessor_id FROM public_keys WHERE id = $1`, rotated.Successor).
		Scan(&predecessor); err != nil {
		t.Fatal(err)
	}
	if predecessor.String != rotated.KeyID {
		t.Fatalf("restored predecessor = %q, want %q", predecessor.String, rotated.KeyID)
	}
	revoked, _, _ := revocationRow(t, targetDB, rotated.Successor)
	if !revoked {
		t.Fatal("restored middle key is not revoked")
	}
}
