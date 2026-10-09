//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// signedForeignBlock is alice on revHomeServerID blocking blocked, signed
// by her key and countersigned by her home server.
func signedForeignBlock(t *testing.T, s signedKeyRevocation, blocked string, signedAt time.Time) BlockCert {
	t.Helper()
	cryptoSvc := newCryptoService()
	userSig, err := cryptoSvc.sign(string(buildBlockUserPayload(s.userID, blocked, s.keyID)), s.userKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverSig, err := cryptoSvc.sign(string(buildBlockServerPayload(s.userID, blocked, s.serverKP.Fingerprint, userSig, signedAt)), s.serverKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return BlockCert{
		Type:            identityTypeBlock,
		UserID:          s.userID,
		BlockedUserID:   blocked,
		UserSignature:   UserSignature{ID: s.keyID, Armor: userSig},
		ServerSignature: ServerSignature{ID: s.serverKeyID, Armor: serverSig, SignedAt: signedAt},
	}
}

func postFromHome(t *testing.T, handler http.HandlerFunc, path string, body proto.Message) int {
	t.Helper()
	req := withPeer(protoRequest(http.MethodPost, path, body), revHomeServerID)
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr.Code
}

func TestBlockNotifyFromPeerStoresAndForcesUnfollow(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	if err := h.services.db.UpsertRemoteIdentity(ctx, s.userID, revHomeServerID); err != nil {
		t.Fatal(err)
	}
	if err := h.services.db.FollowUser(ctx, bob, s.userID); err != nil {
		t.Fatal(err)
	}

	cert := signedForeignBlock(t, s, bob, time.Now().UTC().Truncate(time.Second))
	for range 2 {
		if code := postFromHome(t, h.BlockNotifyFromPeer, "/api/federation/relay/block-notify", blockBody(cert)); code != http.StatusNoContent {
			t.Fatalf("block-notify: status %d", code)
		}
	}
	if got, _ := h.services.db.GetBlock(ctx, s.userID, bob); got == nil {
		t.Fatal("block not stored")
	}
	var n int
	if err := h.services.db.db.QueryRow(`SELECT COUNT(*) FROM user_following WHERE user_id = $1`, bob).Scan(&n); err != nil || n != 0 {
		t.Fatalf("bob still follows alice (%d, %v)", n, err)
	}
	if events, _ := h.services.db.BlockEventsFor(ctx, bob); len(events) != 1 || events[0].Kind != blockEventBlock {
		t.Fatalf("bob owed %+v, want one block", events)
	}
}

func TestBlockNotifyFromPeerRefusesBadCerts(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	signedAt := time.Now().UTC().Truncate(time.Second)
	cert := signedForeignBlock(t, s, bob, signedAt)

	carol := string(canonicalID(h.services.db.GetServerID(), "carol"))
	signedUpUser(t, h, "carol", "carol")
	retargeted := cert
	retargeted.BlockedUserID = carol
	forged := cert
	forged.ServerSignature.Armor = cert.UserSignature.Armor
	notOurs := cert
	notOurs.BlockedUserID = "dave@Other999"

	for name, c := range map[string]BlockCert{
		"retargeted":              retargeted,
		"forged countersignature": forged,
		"blocked user elsewhere":  notOurs,
	} {
		if code := postFromHome(t, h.BlockNotifyFromPeer, "/api/federation/relay/block-notify", blockBody(c)); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	for _, blocked := range []string{bob, carol} {
		if got, _ := h.services.db.GetBlock(context.Background(), s.userID, blocked); got != nil {
			t.Fatalf("a refused block was stored for %s", blocked)
		}
	}
}

func TestUnblockNotifyLiftsTheBlock(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	cert := signedForeignBlock(t, s, bob, time.Now().UTC().Truncate(time.Second))
	if code := postFromHome(t, h.BlockNotifyFromPeer, "/api/federation/relay/block-notify", blockBody(cert)); code != http.StatusNoContent {
		t.Fatalf("block-notify: status %d", code)
	}

	lift := &pb.RelayUnblockPayload{UserId: s.userID, BlockedUserId: bob}
	if code := postFromHome(t, h.UnblockNotifyFromPeer, "/api/federation/relay/unblock-notify", lift); code != http.StatusNoContent {
		t.Fatalf("unblock: status %d", code)
	}
	if got, _ := h.services.db.GetBlock(ctx, s.userID, bob); got != nil {
		t.Fatal("unblock did not lift the block")
	}
	if events, _ := h.services.db.BlockEventsFor(ctx, bob); len(events) != 1 || events[0].Kind != blockEventUnblock {
		t.Fatalf("bob owed %+v, want one lift", events)
	}

	notOurs := &pb.RelayUnblockPayload{UserId: "dave@Other999", BlockedUserId: bob}
	if code := postFromHome(t, h.UnblockNotifyFromPeer, "/api/federation/relay/unblock-notify", notOurs); code != http.StatusBadRequest {
		t.Fatalf("lift of another server's user: status %d", code)
	}
}

func TestNewerForeignBlockReplacesOlder(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	h, bob, _ := s.peerHandlers(t, fake)
	ctx := context.Background()
	older := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	newer := older.Add(30 * time.Minute)

	for _, at := range []time.Time{newer, older} {
		if code := postFromHome(t, h.BlockNotifyFromPeer, "/api/federation/relay/block-notify", blockBody(signedForeignBlock(t, s, bob, at))); code != http.StatusNoContent {
			t.Fatalf("block-notify at %v: status %d", at, code)
		}
	}
	got, _ := h.services.db.GetBlock(ctx, s.userID, bob)
	if got == nil || !got.ServerSignature.SignedAt.Equal(newer) {
		t.Fatalf("stored block = %+v, want the newer one", got)
	}
}

// peerOf registers a connected peer on f's server reachable at baseURL.
func (f blockFixture) peerOf(t *testing.T, peerID, baseURL string) {
	t.Helper()
	if _, err := f.db.Exec(`
		INSERT INTO servers (id, name, self, base_url, frontend_url, connected)
		VALUES ($1, $1, FALSE, $2, $2, TRUE)
	`, peerID, baseURL); err != nil {
		t.Fatal(err)
	}
}

func TestBlockingARemoteUserOwesItsServerUntilAccepted(t *testing.T) {
	f := newBlockFixture(t)
	const peerID = "Pr7mN2qW"
	var status int32 = http.StatusInternalServerError
	var got []string
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		w.WriteHeader(int(atomic.LoadInt32(&status)))
	}))
	t.Cleanup(fake.Close)
	f.h.federationHTTPClientOverride = fake.Client()
	f.peerOf(t, peerID, fake.URL)
	remote := string(canonicalID(peerID, "zed"))

	if rr := f.postBlock(t, f.alice, f.aliceKP, remote); rr.Code != http.StatusOK {
		t.Fatalf("block remote user: status %d %s", rr.Code, rr.Body.String())
	}
	owed := func() int {
		return f.count(t, `SELECT COUNT(*) FROM block_peer_notices WHERE server_id = $1`, peerID)
	}
	f.h.sendOwedBlockNotices(peerID)
	if owed() != 1 {
		t.Fatal("refused notice was dropped")
	}

	atomic.StoreInt32(&status, http.StatusNoContent)
	f.h.sendOwedBlockNotices(peerID)
	if owed() != 0 {
		t.Fatal("accepted notice still owed")
	}
	if !strings.HasSuffix(got[len(got)-1], "/federation/relay/block-notify") {
		t.Fatalf("last call = %s", got[len(got)-1])
	}

	rr := f.serve(t, f.h.UnblockUser, http.MethodDelete, "/api/users/"+remote+"/block", f.alice, f.aliceKP, nil, map[string]string{"userID": remote})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unblock: status %d", rr.Code)
	}
	f.h.sendOwedBlockNotices(peerID)
	if owed() != 0 || !strings.HasSuffix(got[len(got)-1], "/federation/relay/unblock-notify") {
		t.Fatalf("unblock notice not delivered: owed %d, calls %v", owed(), got)
	}
}

func TestPeerLegsRefuseBlockedRequester(t *testing.T) {
	f := newBlockFixture(t)
	const peerID = "Pr7mN2qW"
	f.peerOf(t, peerID, "https://peer.example")
	remote := string(canonicalID(peerID, "zed"))
	if err := f.ds.UpsertRemoteIdentity(context.Background(), remote, peerID); err != nil {
		t.Fatal(err)
	}
	f.storeBlock(t, f.alice, f.aliceKP, remote)
	reedID := f.allocate(t, f.alice, "r1", f.carol)
	f.h.realtimeRelay = newRealtimeService(f.ds, newCryptoService(), "")

	post := func(handler http.HandlerFunc, path string, body proto.Message) *httptest.ResponseRecorder {
		req := withPeer(protoRequest(http.MethodPost, path, body), peerID)
		rr := httptest.NewRecorder()
		handler(rr, req)
		return rr
	}
	_, _, bare, _ := parseKeyFingerprint(identityID(reedID))
	request := &pb.RelayRequestPayload{
		ReedId: bare, AuthorId: f.alice, RequesterUserId: remote,
		RequesterKeyId: string(appendEntity(identityID(remote), "fp")),
		PeerRequestId:  string(appendEntity(identityID(remote), "req")),
	}
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"request":        post(f.h.RelayRequestFromPeer, "/api/federation/relay/request", request),
		"profile-page":   post(f.h.RelayProfilePageFromPeer, "/api/federation/relay/profile-page", &pb.RelayProfilePagePayload{AuthorId: f.alice, RequesterUserId: remote, Page: 1}),
		"subscribe-reed": post(f.h.RelaySubscribeReedFromPeer, "/api/federation/relay/subscribe-reed", &pb.RelaySubscribeReedPayload{ReedId: reedID, RequesterUserId: remote}),
	} {
		if cert := refusalBlock(rr.Body.Bytes()); rr.Code != http.StatusForbidden || cert == nil || cert.BlockedUserID != remote {
			t.Errorf("%s: status %d body %s", name, rr.Code, rr.Body.String())
		}
	}

	req := withPeer(httptest.NewRequest(http.MethodGet, "/api/users/"+f.alice+"/profile?requester="+remote, nil), peerID)
	if viewer := f.h.requestViewer(req); viewer != remote {
		t.Fatalf("peer viewer = %q", viewer)
	}
	spoofed := withPeer(httptest.NewRequest(http.MethodGet, "/api/users/"+f.alice+"/profile?requester="+f.bob, nil), peerID)
	if viewer := f.h.requestViewer(spoofed); viewer != "" {
		t.Fatalf("peer named another server's user: %q", viewer)
	}
}

func blockBody(c BlockCert) *pb.BlockCert { return pbBlockCert(&c) }
