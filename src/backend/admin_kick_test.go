//go:build !ops

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// kick has a1 open and a2 approve a kick of targets.
func (f *adminFixture) kick(t *testing.T, targets ...string) *adminRequest {
	t.Helper()
	msg := f.request(t, "a1", adminActionKick, "Scam links", "", targets...)
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve kick: %d %s", rr.Code, rr.Body.String())
	}
	req, err := f.ds.GetAdminRequest(context.Background(), msg.Id)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// unclaimedInvite stores an invite from inviter nobody has claimed.
func (f *adminFixture) unclaimedInvite(t *testing.T, inviter string) string {
	t.Helper()
	var sigID int64
	seedTestKey(t, f.db, "k")
	if err := f.db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ('k', 's') RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	var serverSigID int64
	if err := f.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id`).Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	id := f.ids[inviter] + "/" + uuid.NewString()
	if _, err := f.db.Exec(`INSERT INTO invites (id, created_by, token_hash, user_signature_id, server_signature_id) VALUES ($1, $2, $3, $4, $5)`,
		id, f.ids[inviter], []byte(id), sigID, serverSigID); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestKick_RemovesTargetsSignedByBothAdmins(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	ctx := context.Background()
	invite := f.unclaimedInvite(t, "u1")
	var kicked []string
	f.h.SetCloseRemovedUserWS(func(userID string) { kicked = append(kicked, userID) })

	req := f.kick(t, "u1", "u2")

	for _, name := range []string{"u1", "u2"} {
		cert, err := f.ds.GetAccountRemoval(ctx, f.ids[name])
		if err != nil || cert == nil {
			t.Fatalf("%s removal = %v %v", name, cert, err)
		}
		if cert.AdminRequestID != req.ID || cert.Admin == nil || cert.Admin.State != adminStateExecuted ||
			cert.Admin.Decision == nil || cert.UserSignature != "" {
			t.Fatalf("%s cert = %+v, want the admin variant", name, cert)
		}
		payload := buildAccountKickServerPayload(f.srv, f.ids[name], req.ID, req.RequesterSignature.Armor,
			req.Decision.Signature.Armor, f.h.signingKey.Fingerprint, cert.ServerSignedAt)
		if err := f.h.services.crypto.verifySignature(ctx, string(payload), cert.ServerSignature, f.h.signingKey.Armor); err != nil {
			t.Fatalf("%s kick countersignature: %v", name, err)
		}
		var username *string
		if err := f.db.QueryRow(`SELECT username FROM users WHERE id = $1`, f.ids[name]).Scan(&username); err != nil || username != nil {
			t.Fatalf("%s username = %v err=%v, want cleared", name, username, err)
		}
	}
	var revoked bool
	if err := f.db.QueryRow(`SELECT revoked_at IS NOT NULL FROM invites WHERE id = $1`, invite).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("kicked user's unclaimed invite revoked=%v err=%v", revoked, err)
	}
	if !slices.Equal(kicked, []string{f.ids["u1"], f.ids["u2"]}) {
		t.Fatalf("closed sockets of %v, want both targets", kicked)
	}
	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u1", nil, nil); rr.Code != http.StatusGone {
		t.Errorf("kicked user's request: %d, want 410", rr.Code)
	}

	rr := f.serve(t, f.h.GetUserProfile, http.MethodGet, "/api/users/"+f.ids["u1"]+"/profile", "a3", nil,
		map[string]string{"userID": f.ids["u1"]})
	var refusal pb.Error
	if err := proto.Unmarshal(rr.Body.Bytes(), &refusal); rr.Code != http.StatusGone || err != nil ||
		refusal.GetAccountRemoval().GetAdmin().GetRequest().GetId() != req.ID {
		t.Errorf("profile of a kicked user: %d %+v err=%v, want 410 with the kick cert", rr.Code, &refusal, err)
	}
}

func TestKick_PendingRequestOnTargetGoesMoot(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick, adminActionSuspend)
	suspend := f.request(t, "a3", adminActionSuspend, "Rude", "", "u1")
	f.openAs(t, "a3", suspend)

	f.kick(t, "u1")

	deadline := time.Now().Add(5 * time.Second)
	for f.state(t, suspend.Id) != adminStateMoot {
		if time.Now().After(deadline) {
			t.Fatalf("pending suspension of a kicked user is %s, want moot", f.state(t, suspend.Id))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestVerifyForeignAdminAuthorization(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	ctx := context.Background()
	req := f.kick(t, "u1")
	auth := func() *pb.AdminAuthorization {
		return &pb.AdminAuthorization{Request: pbAdminRequest(req)}
	}

	if got, err := f.h.verifyForeignAdminAuthorization(ctx, f.srv, auth(), adminActionKick, f.ids["u1"]); err != nil || got.ID != req.ID {
		t.Fatalf("valid kick: %v %v", got, err)
	}
	cases := map[string]func(*pb.AdminAuthorization) (string, string){
		"wrong action":          func(a *pb.AdminAuthorization) (string, string) { return adminActionSuspend, f.ids["u1"] },
		"target not in targets": func(a *pb.AdminAuthorization) (string, string) { return adminActionKick, f.ids["u2"] },
		"tampered reason": func(a *pb.AdminAuthorization) (string, string) {
			a.Request.Reason = "Something else"
			return adminActionKick, f.ids["u1"]
		},
		"rejected": func(a *pb.AdminAuthorization) (string, string) {
			a.Request.Decision.Decision = adminDecisionReject
			return adminActionKick, f.ids["u1"]
		},
		"self-approved": func(a *pb.AdminAuthorization) (string, string) {
			a.Request.Decision.DeciderId = a.Request.RequesterId
			return adminActionKick, f.ids["u1"]
		},
	}
	for name, mutate := range cases {
		a := auth()
		action, target := mutate(a)
		if _, err := f.h.verifyForeignAdminAuthorization(ctx, f.srv, a, action, target); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGetInviteTree_ThreadOrderWithDepths(t *testing.T) {
	f := newAdminFixture(t)
	f.setInvitedBy(t, "a3", "u1")
	f.setInvitedBy(t, "u1", "u2")
	f.useRealExecutors(adminActionSuspend)
	msg := f.request(t, "a2", adminActionSuspend, "Rude", "", "u2")
	f.openAs(t, "a2", msg)
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", rr.Code, rr.Body.String())
	}

	rr := f.serve(t, f.h.GetInviteTree, http.MethodGet, "/api/admin/users/"+f.ids["a3"]+"/invite-tree", "a2", nil,
		map[string]string{"userID": f.ids["a3"]})
	if rr.Code != http.StatusOK {
		t.Fatalf("invite tree: %d %s", rr.Code, rr.Body.String())
	}
	var tree pb.InviteTree
	if err := proto.Unmarshal(rr.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	type node struct {
		id    string
		depth uint32
	}
	var got []node
	for _, n := range tree.Nodes {
		got = append(got, node{n.UserId, n.Depth})
	}
	want := []node{{f.ids["a1"], 0}, {f.ids["a3"], 1}, {f.ids["u1"], 2}, {f.ids["u2"], 3}}
	if !slices.Equal(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	if !tree.Nodes[3].Suspended || tree.Nodes[0].Role != roleAdmin {
		t.Errorf("flags: u2 suspended=%v, a1 role=%s", tree.Nodes[3].Suspended, tree.Nodes[0].Role)
	}
	if rr := f.serve(t, f.h.GetInviteTree, http.MethodGet, "/api/admin/users/"+f.ids["a3"]+"/invite-tree", "u2", nil,
		map[string]string{"userID": f.ids["a3"]}); rr.Code == http.StatusOK {
		t.Errorf("non-admin read the invite tree")
	}
}

func TestForeignKick_KeptOnTheRemovalNotAsARequest(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	ctx := context.Background()
	req := f.kick(t, "u1")
	if _, err := f.db.Exec(`INSERT INTO servers (id, name, frontend_url) VALUES ('peer1', 'peer1', 'https://peer1.example')`); err != nil {
		t.Fatal(err)
	}
	remote := "r1@peer1"
	if err := f.ds.UpsertRemoteIdentity(ctx, remote, "peer1"); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM admin_requests`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	cert := accountRemovalCert{UserID: remote, ServerSignature: "s", ServerFingerprint: "k@peer1",
		ServerSignedAt: time.Now().UTC().Truncate(time.Second), Admin: req}
	if err := f.ds.InsertForeignAccountRemoval(ctx, cert); err != nil {
		t.Fatal(err)
	}
	if err := f.ds.InsertForeignAccountRemoval(ctx, cert); err != nil {
		t.Fatalf("replaying the same kick: %v", err)
	}
	var after int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM admin_requests`).Scan(&after); err != nil || after != before {
		t.Fatalf("admin_requests went from %d to %d (err=%v); a peer's kick isn't a request here", before, after, err)
	}
	stored, err := loadAccountCertTx(ctx, f.db, identityID(remote), false)
	if err != nil || stored.Admin == nil || stored.Admin.ID != req.ID || stored.AdminRequestID != "" ||
		stored.Admin.Decision == nil || stored.Admin.ServerSignature == nil {
		t.Fatalf("stored = %+v err=%v, want the peer's signed approval", stored, err)
	}
}

// A kicked user's signed requests carry the kick's certificate, and their
// handshake is closed with the removed-account code, so a device that was
// offline at the kick still learns why.
func TestKick_KickedUserIsToldWhy(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	f.kick(t, "u1")

	rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u1", nil, nil)
	var refusal pb.Error
	if rr.Code != http.StatusGone || proto.Unmarshal(rr.Body.Bytes(), &refusal) != nil {
		t.Fatalf("kicked user's request: %d %s, want 410", rr.Code, rr.Body.String())
	}
	if got := refusal.GetAccountRemoval().GetAdmin().GetRequest().GetReason(); got != "Scam links" {
		t.Fatalf("refusal reason = %q, want the kick's", got)
	}

	rs := newRealtimeService(f.ds, f.h.services.crypto, "https://syrinx.test")
	srv := httptest.NewServer(http.HandlerFunc(rs.HandleWebSocket))
	t.Cleanup(srv.Close)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "kicked-handshake-nonce"
	payload := string(buildRealtimeAuthPayload(f.srv, f.ids["u1"], ts, nonce))
	sig, err := f.h.services.crypto.sign(context.Background(), payload, f.keys["u1"].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"publicKeyId": {f.keyID("u1")}, "timestamp": {ts}, "nonce": {nonce}, "signature": {base64Encode(sig)}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?"+q.Encode(), http.Header{"Origin": {"https://syrinx.test"}})
	if err != nil {
		t.Fatalf("kicked handshake: %v, want an upgrade that closes", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, realtimeCloseAccountRemoved) {
		t.Fatalf("kicked socket: %v, want close code %d", err, realtimeCloseAccountRemoved)
	}
}
