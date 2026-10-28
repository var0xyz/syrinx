//go:build !ops

package main

import (
	"context"
	"github.com/google/uuid"
	"net/http"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// recordAdminFrames connects each of names and collects the ADMIN_RECORDs
// each one receives.
func (f *adminFixture) recordAdminFrames(t *testing.T, names ...string) map[string][]*pb.AdminRecordMessage {
	t.Helper()
	f.h.realtimeRelay = newRealtimeService(f.ds, newCryptoService(), "")
	got := map[string][]*pb.AdminRecordMessage{}
	for _, name := range names {
		client := newEvictionTestClient(t, f.ids[name], func(_ int, data []byte) {
			var msg pb.WSMessage
			if err := proto.Unmarshal(data, &msg); err != nil {
				t.Errorf("unmarshal outbound frame: %v", err)
				return
			}
			if rec := msg.GetAdminRecord(); rec != nil && msg.GetType() == pb.MessageType_ADMIN_RECORD {
				got[name] = append(got[name], rec)
			}
		})
		f.h.realtimeRelay.connManager.RegisterClient(client)
	}
	return got
}

func TestAdminRecords_PushedToAdminsWhoMaySee(t *testing.T) {
	f := newAdminFixture(t)
	frames := f.recordAdminFrames(t, "root", "a1", "a2", "u1", "u2")
	kinds := func(name string) []string {
		var out []string
		for _, rec := range frames[name] {
			switch rec.GetRecord().(type) {
			case *pb.AdminRecordMessage_Request:
				out = append(out, "request:"+rec.GetRequest().GetState())
			case *pb.AdminRecordMessage_Flag:
				out = append(out, "flag")
			case *pb.AdminRecordMessage_Dismissal:
				out = append(out, "dismissal")
			}
		}
		return out
	}

	userReed, a1Reed := f.localReed(t, "u2"), f.localReed(t, "a1")
	if code := f.flag(t, "u1", userReed, "Scam"); code != 201 {
		t.Fatal(code)
	}
	if code := f.flag(t, "u1", a1Reed, "Rude"); code != 201 {
		t.Fatal(code)
	}
	if rr := f.dismiss(t, "a2", userReed, "Fine"); rr.Code != 200 {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	req := f.request(t, "a1", adminActionSuspend, "Spam", "", "u1")
	f.openAs(t, "a1", req)
	if rr := f.decide(t, "a2", req.Id, adminDecisionReject); rr.Code != 200 {
		t.Fatalf("reject: %d %s", rr.Code, rr.Body.String())
	}

	all := []string{"flag", "flag", "dismissal", "request:pending", "request:rejected"}
	for name, want := range map[string][]string{
		"root": all,
		"a2":   all,
		// Not the flag on a1's own reed.
		"a1": {"flag", "dismissal", "request:pending", "request:rejected"},
		"u1": nil,
		"u2": nil,
	} {
		if got := kinds(name); !slices.Equal(got, want) {
			t.Errorf("%s got %v, want %v", name, got, want)
		}
	}
}

// requestState is id's state on s, and its closing signature.
func requestState(t *testing.T, s *e2eServer, id string) (string, string) {
	t.Helper()
	req, err := s.h.services.db.GetAdminRequest(context.Background(), id)
	if err != nil || req == nil {
		t.Fatalf("request %s: %v", id, err)
	}
	if req.ServerSignature == nil {
		return req.State, ""
	}
	return req.State, req.ServerSignature.Armor
}

// A request still pending on one device is held, and can't be decided while
// recovering, until another device's closed copy settles it.
func TestRecoveryE2E_PendingRequestsWaitForTheirClosedCopy(t *testing.T) {
	sc := newRecoveryScenario(t)
	id := queryRows(t, sc.a.db, `SELECT id FROM admin_requests WHERE state = 'pending'`)[0]
	before := map[*e2eUser]device{}
	for _, u := range sc.everyone {
		before[u] = sc.deviceOf(t, u)
	}
	sc.a.decide(t, sc.root, id, adminDecisionReject)
	closedState, closedSig := requestState(t, sc.a, id)
	daveAfter := sc.deviceOf(t, sc.dave)
	// first_boot_at is to the second: rebuild in a later one than the request.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))

	t.Run("closed after pending", func(t *testing.T) {
		b := restoreIdentity(t, sc.a)
		for _, u := range sc.everyone {
			b.recover(t, before[u])
		}
		if state, _ := requestState(t, b, id); state != adminStatePending {
			t.Fatalf("restored state = %s, want pending", state)
		}
		req, _ := b.h.services.db.GetAdminRequest(context.Background(), id)
		sig := b.sign(t, sc.root, buildAdminDecisionPayload(id, adminDecisionApprove, sc.root.id, sc.root.keyID, req.RequesterSignature.Armor))
		msg := &pb.AdminDecision{Decision: adminDecisionApprove, DeciderId: sc.root.id, Signature: &pb.UserSignature{Id: sc.root.keyID, Armor: sig}}
		rr := b.authed(t, sc.root, http.MethodPost, "/api/admin/requests/"+id+"/decision", msg, map[string]string{"id": id}, b.h.DecideAdminRequest)
		if rr.Code != http.StatusConflict {
			t.Errorf("deciding a request opened before recovery: %d, want 409", rr.Code)
		}

		b.reportRecords(t, sc.dave, slices.All(daveAfter.records))
		if state, sig := requestState(t, b, id); state != closedState || sig != closedSig {
			t.Errorf("after the closed copy: %s, want %s with its signature", state, closedState)
		}
	})

	t.Run("pending after closed", func(t *testing.T) {
		b := restoreIdentity(t, sc.a)
		b.recover(t, daveAfter)
		for _, u := range sc.everyone {
			if u != sc.dave {
				b.recover(t, before[u])
			}
		}
		if state, sig := requestState(t, b, id); state != closedState || sig != closedSig {
			t.Errorf("a pending copy reopened it: %s, want %s", state, closedState)
		}
	})

	t.Run("decided once recovery is off", func(t *testing.T) {
		b := restoreIdentity(t, sc.a)
		for _, u := range sc.everyone {
			b.recover(t, before[u])
		}
		b.h.cfg.RecoveryMode = string(recoveryModeOff)
		b.decide(t, sc.root, id, adminDecisionReject)
		if state, _ := requestState(t, b, id); state != adminStateRejected {
			t.Errorf("state = %s, want rejected", state)
		}
	})
}

// A flag a peer's member raised comes back from an admin's device once the
// reporter's key is held here, and waits until it is.
func TestRecoveryE2E_RemoteReporterFlagWaitsForTheirKey(t *testing.T) {
	sc := newRecoveryScenario(t)
	b := restoreIdentity(t, sc.a)
	for _, u := range sc.everyone {
		b.recover(t, sc.deviceOf(t, u))
	}
	ctx := context.Background()
	reedID := queryRows(t, b.db, `SELECT id FROM reed_identities WHERE author_id = '`+sc.alice.id+`' ORDER BY id LIMIT 1`)[0]
	const peerID = "Pr7mN2qW"
	if _, err := b.db.Exec(`INSERT INTO servers (id, name, self, base_url, frontend_url) VALUES ($1, 'peer', FALSE, 'https://peer.example', 'https://peer.example')`, peerID); err != nil {
		t.Fatal(err)
	}
	reporter := string(canonicalID(peerID, "zed"))
	kp, err := b.h.services.crypto.createKeyPair(reporter, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	keyID := string(appendEntity(identityID(reporter), kp.Fingerprint))
	f := reedFlag{ReedID: reedID, ReporterID: reporter, Note: "spam", CaseID: uuid.NewString(),
		Signature: UserSignature{ID: keyID, Armor: b.sign(t, &e2eUser{kp: *kp}, buildReedFlagPayload(reedID, reporter, keyID, "spam"))}}
	now := time.Now().UTC().Truncate(time.Second)
	if f.ServerSignature, err = b.h.countersign(ctx, buildReedFlagServerPayload(reedID, reporter, f.CaseID, b.h.signingKey.Fingerprint, f.Signature.Armor, now), now); err != nil {
		t.Fatal(err)
	}
	report := func() string {
		rr := b.authed(t, sc.root, http.MethodPost, "/api/recovery/records",
			&pb.RecoveryRecordsRequest{Records: []*pb.RecoveryRecord{{Record: &pb.RecoveryRecord_Flag{Flag: pbReedFlag(f)}}}}, nil, b.h.ReportRecords)
		expectStatus(t, "records", rr, http.StatusOK)
		var res pb.RecoveryRecordsResponse
		decodeProto(t, rr.Body.Bytes(), &res)
		return res.GetResults()[0].GetStatus()
	}

	if got := report(); got != recoveryRecordPending {
		t.Fatalf("without the reporter's key: %s, want pending", got)
	}
	seedTestKey(t, b.db, keyID)
	if _, err := b.db.Exec(`UPDATE public_keys SET armor = $2 WHERE id = $1`, keyID, kp.PublicKey); err != nil {
		t.Fatal(err)
	}
	if got := report(); got != recoveryRecordStored {
		t.Fatalf("with the reporter's key: %s, want stored", got)
	}
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM reed_flags WHERE reporter_id = '`+reporter+`'`); n[0] != "1" {
		t.Errorf("flags by the remote reporter = %s, want 1", n[0])
	}
}

// Holding a removed user's key is enough to be told of the removal on
// catch-up, until the device acks it.
func TestAccountRemoval_ReachesKeyHolders(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	ctx := context.Background()
	if err := f.ds.AllocatePublicKey(ctx, f.ids["a3"], f.keyID("u1")); err != nil {
		t.Fatal(err)
	}
	f.kick(t, "u1")

	removed := func(viewer string) bool {
		missing, err := f.ds.GetMissingAccountRemovals(ctx, f.ids[viewer])
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(missing, func(m missingAccountRemoval) bool { return m.UserID == f.ids["u1"] })
	}
	if !removed("a3") {
		t.Error("a3 holds u1's key but isn't told of the removal")
	}
	if removed("root") {
		t.Error("root holds nothing of u1's but is told of the removal")
	}
	if _, err := f.ds.ClearPeerStateForRemovedAccount(ctx, f.ids["a3"], f.ids["u1"]); err != nil {
		t.Fatal(err)
	}
	if removed("a3") {
		t.Error("a3 is still told of the removal after acking it")
	}
}
