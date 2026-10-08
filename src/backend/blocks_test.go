//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// blockFixture is alice (the blocking user), bob (blocked) and carol, all local
// users with real keypairs.
type blockFixture struct {
	h                 *Handlers
	ds                *DataService
	db                *sql.DB
	alice, bob, carol string
	aliceKP, bobKP    cryptoKeyPair
	carolKP           cryptoKeyPair
}

func newBlockFixture(t *testing.T) blockFixture {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	ds := h.services.db
	srv := ds.GetServerID()
	f := blockFixture{h: h, ds: ds, db: db}
	f.aliceKP = signedUpUser(t, h, "alice", "alice")
	f.bobKP = signedUpUser(t, h, "bob", "bob")
	f.carolKP = signedUpUser(t, h, "carol", "carol")
	f.alice = string(canonicalID(srv, "alice"))
	f.bob = string(canonicalID(srv, "bob"))
	f.carol = string(canonicalID(srv, "carol"))
	return f
}

func (f blockFixture) keyID(userID string, kp cryptoKeyPair) string {
	return string(appendEntity(identityID(userID), kp.Fingerprint))
}

// signedBlock returns blocking user's user signature over a block of blocked.
func (f blockFixture) signedBlock(t *testing.T, user string, kp cryptoKeyPair, blocked string) (keyID, sig string) {
	t.Helper()
	keyID = f.keyID(user, kp)
	sig, err := f.h.services.crypto.sign(string(buildBlockUserPayload(user, blocked, keyID)), kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return keyID, sig
}

// storeBlock countersigns and stores blocking user's block of blocked.
func (f blockFixture) storeBlock(t *testing.T, user string, kp cryptoKeyPair, blocked string) BlockCert {
	t.Helper()
	keyID, sig := f.signedBlock(t, user, kp, blocked)
	now := time.Now().UTC().Truncate(time.Second)
	serverSig, err := f.h.countersign(buildBlockServerPayload(user, blocked, f.h.signingKey.Fingerprint, sig, now), now)
	if err != nil {
		t.Fatal(err)
	}
	cert := BlockCert{
		Type:            identityTypeBlock,
		UserID:          user,
		BlockedUserID:   blocked,
		UserSignature:   UserSignature{ID: keyID, Armor: sig},
		ServerSignature: serverSig,
	}
	if _, err := f.ds.InsertBlock(context.Background(), cert); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestInsertBlockIsIdempotent(t *testing.T) {
	f := newBlockFixture(t)
	ctx := context.Background()

	cert := f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	created, err := f.ds.InsertBlock(ctx, cert)
	if err != nil || created {
		t.Fatalf("replay: created=%v err=%v, want false nil", created, err)
	}

	got, err := f.ds.GetBlock(ctx, f.alice, f.bob)
	if err != nil || got == nil {
		t.Fatalf("GetBlock: %v %v", got, err)
	}
	if got.UserSignature != cert.UserSignature || got.ServerSignature.Armor != cert.ServerSignature.Armor ||
		!got.ServerSignature.SignedAt.Equal(cert.ServerSignature.SignedAt) || got.Type != identityTypeBlock {
		t.Fatalf("stored cert = %+v, want %+v", got, cert)
	}

	other := cert
	other.UserSignature.Armor = "different"
	if _, err := f.ds.InsertBlock(ctx, other); !errors.Is(err, ErrBlockConflict) {
		t.Fatalf("different signature: err=%v, want ErrBlockConflict", err)
	}
	if got, _ := f.ds.GetBlock(ctx, f.bob, f.alice); got != nil {
		t.Fatalf("reverse direction found a block: %+v", got)
	}
}

func TestInsertBlockRefusesSelf(t *testing.T) {
	f := newBlockFixture(t)
	cert := BlockCert{
		UserID:          f.alice,
		BlockedUserID:   f.alice,
		UserSignature:   UserSignature{ID: f.keyID(f.alice, f.aliceKP), Armor: "s"},
		ServerSignature: ServerSignature{ID: "x", Armor: "s", SignedAt: time.Now()},
	}
	if _, err := f.ds.InsertBlock(context.Background(), cert); err == nil {
		t.Fatal("self block stored")
	}
}
