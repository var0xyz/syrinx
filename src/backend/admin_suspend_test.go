//go:build !ops

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// useRealExecutors drops the fixture's stand-ins for actions, so their
// real executors run.
func (f *adminFixture) useRealExecutors(actions ...string) {
	for _, a := range actions {
		delete(f.h.adminExecutorOverride, a)
	}
}

// suspend has a1 open and a2 approve a suspension of targets.
func (f *adminFixture) suspend(t *testing.T, targets ...string) string {
	t.Helper()
	msg := f.request(t, "a1", adminActionSuspend, "Abusive replies", "", targets...)
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve suspension: %d %s", rr.Code, rr.Body.String())
	}
	return msg.Id
}

func TestSuspend_RefusesSignedRequestsWithCertificate(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionSuspend, adminActionReinstate)
	var kicked []string
	f.h.SetKickUserWS(func(userID string) { kicked = append(kicked, userID) })
	requestID := f.suspend(t, "u1")
	if len(kicked) != 1 || kicked[0] != f.ids["u1"] {
		t.Fatalf("closed sockets of %v, want u1", kicked)
	}

	rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u1", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("suspended ledger read: %d, want 403", rr.Code)
	}
	var refusal pb.Error
	if err := proto.Unmarshal(rr.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	cert := refusal.GetSuspension().GetRequest()
	if cert.GetId() != requestID || cert.GetReason() != "Abusive replies" || cert.GetState() != adminStateExecuted ||
		cert.GetDecision().GetDeciderId() != f.ids["a2"] || cert.GetServerSignature() == nil {
		t.Fatalf("suspension certificate = %+v", cert)
	}

	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u2", nil, nil); rr.Code != http.StatusOK {
		t.Errorf("another user's ledger read: %d, want 200", rr.Code)
	}
}

func TestSuspend_AlreadySuspendedRefusedAtOpen(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionSuspend, adminActionReinstate)
	f.suspend(t, "u1")

	again := f.request(t, "a1", adminActionSuspend, "Again", "", "u1", "u2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", again, nil); rr.Code != http.StatusConflict {
		t.Fatalf("suspending a suspended user: %d, want 409", rr.Code)
	}
	reinstateUnsuspended := f.request(t, "a1", adminActionReinstate, "Fine", "", "u2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", reinstateUnsuspended, nil); rr.Code != http.StatusConflict {
		t.Fatalf("reinstating someone not suspended: %d, want 409", rr.Code)
	}
}

func TestReinstate_RestoresAccess(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionSuspend, adminActionReinstate)
	f.suspend(t, "u1", "u2")

	msg := f.request(t, "a1", adminActionReinstate, "Talked it through", "", "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve reinstate: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u1", nil, nil); rr.Code != http.StatusOK {
		t.Errorf("reinstated user's read: %d, want 200", rr.Code)
	}
	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u2", nil, nil); rr.Code != http.StatusForbidden {
		t.Errorf("still suspended user's read: %d, want 403", rr.Code)
	}
}

func TestSuspend_WebSocketHandshakeRefused(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionSuspend)
	f.suspend(t, "u1")

	rs := newRealtimeService(f.ds, f.h.services.crypto, "*")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "suspended-handshake-nonce"
	payload := string(buildRealtimeAuthPayload(f.srv, f.ids["u1"], ts, nonce))
	sig, err := f.h.services.crypto.sign(payload, f.keys["u1"].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"publicKeyId": {f.keyID("u1")}, "timestamp": {ts}, "nonce": {nonce}, "signature": {base64Encode(sig)}}
	rr := httptest.NewRecorder()
	rs.HandleWebSocket(rr, httptest.NewRequest(http.MethodGet, "/ws/?"+q.Encode(), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("suspended handshake: %d %s, want 403", rr.Code, rr.Body.String())
	}
	var refusal pb.Error
	if err := proto.Unmarshal(rr.Body.Bytes(), &refusal); err != nil || refusal.GetSuspension() == nil {
		t.Fatalf("handshake refusal = %+v err=%v, want the suspension certificate", &refusal, err)
	}

	got, err := f.ds.GetSuspension(context.Background(), f.ids["u2"])
	if err != nil || got != nil {
		t.Fatalf("u2 suspension = %+v err=%v, want none", got, err)
	}
}

func TestSuspend_InvitesUnclaimableUntilReinstated(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionSuspend, adminActionReinstate)
	ctx := context.Background()
	invite := f.unclaimedInvite(t, "u1")
	pending := func() bool {
		t.Helper()
		inv, err := getPendingInvite(ctx, f.db, f.db, invite, []byte(invite))
		if err != nil {
			t.Fatalf("getPendingInvite: %v", err)
		}
		return inv != nil
	}

	f.suspend(t, "u1")
	if pending() {
		t.Fatal("a suspended inviter's invite still claimable")
	}
	msg := f.request(t, "a1", adminActionReinstate, "Talked it through", "", "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve reinstate: %d %s", rr.Code, rr.Body.String())
	}
	if !pending() {
		t.Fatal("invite not claimable after reinstating its inviter")
	}
}
