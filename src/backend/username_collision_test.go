//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	pb "syrinx/proto"
)

// Golden bytes shared with scripts/test-username-collision-payload.mjs.
func TestBuildUsernameCollisionPayload(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	got := string(buildUsernameCollisionPayload("home1234", "alice", "old@home1234", "new@home1234", at))
	want := `{"loserID":"new@home1234","serverID":"home1234","signedAt":"2026-10-23T12:00:00Z",` +
		`"type":"username-collision","username":"alice","winnerID":"old@home1234"}`
	if got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
}

// A user from before the wipe takes their username back from a newcomer,
// who keeps the account, gets a signed record of why, and renames.
func TestUsernameCollisionE2E(t *testing.T) {
	ctx := context.Background()
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	alice := a.signup(t, "alice")
	snap := a.snapshot(t, alice.id)
	// Signatures have second precision; the wipe comes a second later.
	time.Sleep(time.Second)

	b := restoreIdentity(t, a)
	b.h.cfg.RecoveryMode = string(recoveryModeOff)
	newcomer := b.signup(t, "alice")
	b.claim(t, alice, snap)

	profile := func(u *e2eUser) *pb.User {
		t.Helper()
		rr := b.authed(t, u, http.MethodGet, "/api/users/"+u.id+"/profile", nil, map[string]string{"userID": u.id}, b.h.GetUserProfile)
		expectStatus(t, "profile", rr, http.StatusOK)
		var user pb.User
		decodeProto(t, rr.Body.Bytes(), &user)
		return &user
	}
	if got := profile(alice).GetUsername(); got != "alice" {
		t.Fatalf("returning user's username = %q", got)
	}
	lost := profile(newcomer)
	c := lost.GetUsernameCollision()
	if lost.GetUsername() != "" || c.GetUsername() != "alice" || c.GetWinnerId() != alice.id || c.GetLoserId() != newcomer.id {
		t.Fatalf("newcomer's profile = %q, collision %+v", lost.GetUsername(), c)
	}
	sig := c.GetServerSignature()
	key, err := b.h.services.db.GetPublicKey(ctx, sig.GetId())
	if err != nil || key == nil {
		t.Fatalf("signing key %q: %v", sig.GetId(), err)
	}
	payload := buildUsernameCollisionPayload(b.h.services.db.GetServerID(), c.GetUsername(), c.GetWinnerId(), c.GetLoserId(),
		timeFromUnix(sig.GetSignedAt()))
	if err := b.h.services.crypto.verifySignature(ctx, string(payload), sig.GetArmor(), key.Armor); err != nil {
		t.Fatalf("collision record does not verify: %v", err)
	}

	userSig, err := b.h.services.crypto.sign(ctx, string(buildUserIdentityPayload("alice2", newcomer.keyID, "")), newcomer.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	rr := b.authed(t, newcomer, http.MethodPut, "/api/users/me", &pb.UpdateUserRequest{Username: "alice2", UserSignature: userSig}, nil, b.h.UpdateUser)
	expectStatus(t, "rename", rr, http.StatusOK)
	if renamed := profile(newcomer); renamed.GetUsername() != "alice2" || renamed.GetUsernameCollision() != nil {
		t.Fatalf("after rename = %q, collision %+v", renamed.GetUsername(), renamed.GetUsernameCollision())
	}
}
