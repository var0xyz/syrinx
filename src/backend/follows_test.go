//go:build !ops

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	pb "syrinx/proto"
)

// seedFollowEdges adds the edges of a follow, without a certificate.
func seedFollowEdges(ds *DataService, followerID, followeeID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ds.setFollowEdgesTx(context.Background(), tx, followerID, followeeID, true); err != nil {
		return err
	}
	return tx.Commit()
}

// follow signs u's follow (or unfollow) of target on s and returns the
// countersigned certificate.
func (s *e2eServer) followAs(t *testing.T, u *e2eUser, target string, follow bool) *pb.FollowCert {
	t.Helper()
	_, _, fp, _ := parseKeyFingerprint(identityID(u.keyID))
	sig, err := s.h.services.crypto.sign(context.Background(), string(buildFollowUserPayload(u.id, target, u.keyID, follow)), u.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	method, handler := http.MethodPost, s.h.FollowUser
	if !follow {
		method, handler = http.MethodDelete, s.h.UnfollowUser
	}
	rr := s.authed(t, u, method, "/api/users/"+target+"/follow", &pb.FollowRequest{Signature: sig, Fingerprint: fp}, map[string]string{"userID": target}, handler)
	expectStatus(t, "follow", rr, http.StatusOK)
	var cert pb.FollowCert
	decodeProto(t, rr.Body.Bytes(), &cert)
	return &cert
}

func countRows(t *testing.T, s *e2eServer, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A local follow is countersigned, sets both edges and is owed to the
// followee; their ack settles it. An unfollow is kept only until acked.
func TestFollowCertLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newE2EServer(t, newTestDatabase(t, InitDB), false)
	ann, bob := s.signup(t, "ann"), s.signup(t, "bob")

	cert := s.followAs(t, ann, bob.id, true)
	if !cert.GetFollow() || cert.GetServerSignature().GetArmor() == "" {
		t.Fatalf("cert = %+v", cert)
	}
	sig := cert.GetServerSignature()
	key, err := s.h.services.db.GetPublicKey(ctx, sig.GetId())
	if err != nil || key == nil {
		t.Fatalf("server key: %v", err)
	}
	payload := buildFollowServerPayload(ann.id, bob.id, true, s.h.signingKey.Fingerprint, cert.GetUserSignature().GetArmor(), timeFromUnix(sig.GetSignedAt()))
	if err := s.h.services.crypto.verifySignature(ctx, string(payload), sig.GetArmor(), key.Armor); err != nil {
		t.Fatalf("countersignature: %v", err)
	}
	if n := countRows(t, s, `SELECT (SELECT COUNT(*) FROM user_following WHERE user_id = $1 AND following_user_id = $2) + (SELECT COUNT(*) FROM user_followers WHERE user_id = $2 AND follower_user_id = $1)`, ann.id, bob.id); n != 2 {
		t.Fatalf("edges = %d", n)
	}
	owed, err := s.h.services.db.FollowEventsFor(ctx, bob.id)
	if err != nil || len(owed) != 1 || !owed[0].Follow {
		t.Fatalf("owed to bob = %+v (%v)", owed, err)
	}
	list, err := s.h.services.db.ListFollowers(ctx, bob.id, 10, nil)
	if err != nil || len(list.Users) != 1 || list.Users[0].Cert == nil {
		t.Fatalf("followers = %+v (%v)", list, err)
	}
	if err := s.h.services.db.AckFollowEvent(ctx, ann.id, bob.id, true); err != nil {
		t.Fatal(err)
	}

	// Within the same second the unfollow wouldn't be newer.
	time.Sleep(time.Second)
	s.followAs(t, ann, bob.id, false)
	if n := countRows(t, s, `SELECT COUNT(*) FROM user_followers WHERE user_id = $1`, bob.id); n != 0 {
		t.Fatalf("followers after unfollow = %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM follow_certs WHERE NOT follow`); n != 1 {
		t.Fatalf("unfollow kept before ack: %d", n)
	}
	// An ack for the follow it replaced settles nothing.
	if err := s.h.services.db.AckFollowEvent(ctx, ann.id, bob.id, true); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM follow_event_queue`); n != 1 {
		t.Fatalf("events after stale ack = %d", n)
	}
	if err := s.h.services.db.AckFollowEvent(ctx, ann.id, bob.id, false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM follow_certs`); n != 0 {
		t.Fatalf("certs after the unfollow's ack = %d", n)
	}
}

// An older certificate never replaces a newer one.
func TestSaveFollowCertKeepsNewest(t *testing.T) {
	ctx := context.Background()
	s := newE2EServer(t, newTestDatabase(t, InitDB), false)
	ann, bob := s.signup(t, "ann"), s.signup(t, "bob")
	cert := followCertFromPB(s.followAs(t, ann, bob.id, true))

	older := cert
	older.Follow = false
	older.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.Add(-time.Minute)
	applied, err := s.h.services.db.SaveFollowCert(ctx, older, true)
	if err != nil || applied {
		t.Fatalf("older cert applied = %v (%v)", applied, err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM user_followers WHERE user_id = $1`, bob.id); n != 1 {
		t.Fatalf("followers = %d", n)
	}
}

// signedForeignFollow is alice on revHomeServerID following (or with follow
// false unfollowing) followee, countersigned by her home server.
func signedForeignFollow(t *testing.T, s signedKeyRevocation, followee string, follow bool, signedAt time.Time) FollowCert {
	t.Helper()
	cryptoSvc := newCryptoService()
	userSig, err := cryptoSvc.sign(context.Background(), string(buildFollowUserPayload(s.userID, followee, s.keyID, follow)), s.userKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverSig, err := cryptoSvc.sign(context.Background(), string(buildFollowServerPayload(s.userID, followee, follow, s.serverKP.Fingerprint, userSig, signedAt)), s.serverKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return FollowCert{
		FollowerID:      s.userID,
		FolloweeID:      followee,
		Follow:          follow,
		UserSignature:   UserSignature{ID: s.keyID, Armor: userSig},
		ServerSignature: ServerSignature{ID: s.serverKeyID, Armor: serverSig, SignedAt: signedAt},
	}
}

// A peer's follow of a local user is verified, sets the follower edge and
// is owed to the followee; a forged one is refused.
func TestFollowNotifyFromPeer(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	follow := signedForeignFollow(t, s, bob, true, at)

	forged := follow
	forged.ServerSignature.Armor = follow.UserSignature.Armor
	if code := postFromHome(t, h.FollowNotifyFromPeer, "/api/federation/relay/follow-notify", pbFollowCert(&forged)); code != http.StatusBadRequest {
		t.Fatalf("forged follow: status %d", code)
	}
	if code := postFromHome(t, h.FollowNotifyFromPeer, "/api/federation/relay/follow-notify", pbFollowCert(&follow)); code != http.StatusNoContent {
		t.Fatalf("follow-notify: status %d", code)
	}
	var n int
	if err := h.services.db.db.QueryRow(`SELECT COUNT(*) FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, bob, s.userID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("followers = %d (%v)", n, err)
	}
	if owed, _ := h.services.db.FollowEventsFor(ctx, bob); len(owed) != 1 || !owed[0].Follow {
		t.Fatalf("bob owed %+v", owed)
	}

	unfollow := signedForeignFollow(t, s, bob, false, at.Add(time.Second))
	if code := postFromHome(t, h.FollowNotifyFromPeer, "/api/federation/relay/follow-notify", pbFollowCert(&unfollow)); code != http.StatusNoContent {
		t.Fatalf("unfollow-notify: status %d", code)
	}
	if err := h.services.db.db.QueryRow(`SELECT COUNT(*) FROM user_followers WHERE user_id = $1`, bob).Scan(&n); err != nil || n != 0 {
		t.Fatalf("followers after unfollow = %d (%v)", n, err)
	}
	if owed, _ := h.services.db.FollowEventsFor(ctx, bob); len(owed) != 1 || owed[0].Follow {
		t.Fatalf("bob owed %+v, want the unfollow", owed)
	}
}
