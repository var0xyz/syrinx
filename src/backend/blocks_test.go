//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/mux"
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
	if _, _, err := f.ds.InsertBlock(context.Background(), cert); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestInsertBlockIsIdempotent(t *testing.T) {
	f := newBlockFixture(t)
	ctx := context.Background()

	cert := f.storeBlock(t, f.alice, f.aliceKP, f.bob)
	created, _, err := f.ds.InsertBlock(ctx, cert)
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
	if _, _, err := f.ds.InsertBlock(ctx, other); !errors.Is(err, ErrBlockConflict) {
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
	if _, _, err := f.ds.InsertBlock(context.Background(), cert); err == nil {
		t.Fatal("self block stored")
	}
}

// serve drives handler through the real auth middleware as userID.
func (f blockFixture) serve(t *testing.T, handler http.HandlerFunc, method, path, userID string, kp cryptoKeyPair, form url.Values, vars map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := signedRequest(t, f.h, method, path, userID, kp.Fingerprint, kp.PrivateKey, form)
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	f.h.signatureAuthMiddleware("/api")(handler).ServeHTTP(rr, req)
	return rr
}

// postBlock has blocking user block blocked through BlockUser.
func (f blockFixture) postBlock(t *testing.T, user string, kp cryptoKeyPair, blocked string) *httptest.ResponseRecorder {
	t.Helper()
	_, sig := f.signedBlock(t, user, kp, blocked)
	form := url.Values{"signature": {sig}, "fingerprint": {kp.Fingerprint}}
	return f.serve(t, f.h.BlockUser, http.MethodPost, "/api/users/"+blocked+"/block", user, kp, form, map[string]string{"userID": blocked})
}

// allocate records holder holding a reed authored by author.
func (f blockFixture) allocate(t *testing.T, author, reedID, holder string) string {
	t.Helper()
	id := string(appendEntity(identityID(author), reedID))
	if _, err := f.db.Exec(`INSERT INTO reed_identities (id, server_id, author_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, f.ds.GetServerID(), author); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO reed_allocations (reed_id, holder_user_id) VALUES ($1, $2)`, id, holder); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f blockFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBlockUserForcesUnfollowAndDropsAllocations(t *testing.T) {
	f := newBlockFixture(t)
	ctx := context.Background()
	for _, pair := range [][2]string{{f.bob, f.alice}, {f.alice, f.bob}, {f.carol, f.alice}} {
		if err := f.ds.FollowUser(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	f.allocate(t, f.alice, "r1", f.bob)
	f.allocate(t, f.alice, "r1", f.carol)
	f.allocate(t, f.carol, "r2", f.bob)

	rr := f.postBlock(t, f.alice, f.aliceKP, f.bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("block: status %d %s", rr.Code, rr.Body.String())
	}
	var cert BlockCert
	if err := json.Unmarshal(rr.Body.Bytes(), &cert); err != nil {
		t.Fatal(err)
	}
	if cert.Type != identityTypeBlock || cert.UserID != f.alice || cert.BlockedUserID != f.bob {
		t.Fatalf("cert = %+v", cert)
	}
	payload := buildBlockServerPayload(f.alice, f.bob, f.h.signingKey.Fingerprint, cert.UserSignature.Armor, cert.ServerSignature.SignedAt)
	if err := f.h.services.crypto.verifySignature(string(payload), cert.ServerSignature.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("countersignature: %v", err)
	}

	if n := f.count(t, `SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2`, f.bob, f.alice); n != 0 {
		t.Fatal("bob still follows alice")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, f.alice, f.bob); n != 0 {
		t.Fatal("bob still a follower of alice")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2`, f.alice, f.bob); n != 1 {
		t.Fatal("alice's follow of bob was touched")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_following WHERE user_id = $1`, f.carol); n != 1 {
		t.Fatal("carol's follow was touched")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM reed_allocations WHERE holder_user_id = $1`, f.bob); n != 1 {
		t.Fatalf("bob holds %d allocations, want only carol's reed", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM reed_allocations WHERE holder_user_id = $1`, f.carol); n != 1 {
		t.Fatal("carol's allocation was dropped")
	}

	again := f.postBlock(t, f.alice, f.aliceKP, f.bob)
	if again.Code != http.StatusOK && again.Code != http.StatusConflict {
		t.Fatalf("replay: status %d", again.Code)
	}
}

func TestBlockUserRefusals(t *testing.T) {
	f := newBlockFixture(t)
	if rr := f.postBlock(t, f.alice, f.aliceKP, f.alice); rr.Code != http.StatusBadRequest {
		t.Fatalf("self: status %d", rr.Code)
	}
	ghost := string(canonicalID(f.ds.GetServerID(), "ghost"))
	if rr := f.postBlock(t, f.alice, f.aliceKP, ghost); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown: status %d", rr.Code)
	}

	_, sig := f.signedBlock(t, f.alice, f.aliceKP, f.carol)
	form := url.Values{"signature": {sig}, "fingerprint": {f.aliceKP.Fingerprint}}
	rr := f.serve(t, f.h.BlockUser, http.MethodPost, "/api/users/"+f.bob+"/block", f.alice, f.aliceKP, form, map[string]string{"userID": f.bob})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("signature over another user: status %d", rr.Code)
	}
}

func TestUnblockAndList(t *testing.T) {
	f := newBlockFixture(t)
	if rr := f.postBlock(t, f.alice, f.aliceKP, f.bob); rr.Code != http.StatusOK {
		t.Fatalf("block bob: %d", rr.Code)
	}
	if rr := f.postBlock(t, f.alice, f.aliceKP, f.carol); rr.Code != http.StatusOK {
		t.Fatalf("block carol: %d", rr.Code)
	}

	rr := f.serve(t, f.h.ListMyBlocks, http.MethodGet, "/api/blocks", f.alice, f.aliceKP, nil, nil)
	var list struct{ Blocks []BlockCert }
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list.Blocks) != 2 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.serve(t, f.h.ListMyBlocks, http.MethodGet, "/api/blocks", f.bob, f.bobKP, nil, nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list.Blocks) != 0 {
		t.Fatalf("bob's list: %s", rr.Body.String())
	}

	for range 2 {
		rr = f.serve(t, f.h.UnblockUser, http.MethodDelete, "/api/users/"+f.bob+"/block", f.alice, f.aliceKP, nil, map[string]string{"userID": f.bob})
		if rr.Code != http.StatusNoContent {
			t.Fatalf("unblock: status %d", rr.Code)
		}
	}
	if b, _ := f.ds.GetBlock(context.Background(), f.alice, f.bob); b != nil {
		t.Fatal("block survived unblock")
	}
	if b, _ := f.ds.GetBlock(context.Background(), f.alice, f.carol); b == nil {
		t.Fatal("unblocking bob lifted carol's block")
	}
}
