//go:build !ops

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// ownFlag fetches reporter's own flag on reedID.
func (f *adminFixture) ownFlag(t *testing.T, reporter, reedID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.serve(t, f.h.GetOwnFlag, http.MethodGet, "/api/flags/"+reedID, reporter, nil, map[string]string{"reedID": reedID})
}

// withdraw has reporter sign a withdrawal over flagSignature and post it.
func (f *adminFixture) withdraw(t *testing.T, reporter, reedID, flagSignature string) *httptest.ResponseRecorder {
	t.Helper()
	payload := buildFlagWithdrawalPayload(reedID, f.ids[reporter], f.keyID(reporter), flagSignature)
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[reporter].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.FlagWithdrawal{Signature: &pb.UserSignature{Id: f.keyID(reporter), Armor: sig}}
	return f.serve(t, f.h.WithdrawOwnFlag, http.MethodPost, "/api/flags/"+reedID+"/withdraw", reporter, msg, map[string]string{"reedID": reedID})
}

// flagged flags reedID as reporter and returns the countersigned flag.
func (f *adminFixture) flagged(t *testing.T, reporter, reedID string) *pb.ReedFlag {
	t.Helper()
	rr := f.postFlag(t, reporter, reedID, "Spam")
	if rr.Code != http.StatusCreated {
		t.Fatalf("flag: %d %s", rr.Code, rr.Body.String())
	}
	var out pb.ReedFlag
	if err := proto.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func decodeFlag(t *testing.T, rr *httptest.ResponseRecorder) *pb.ReedFlag {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s, want 200", rr.Code, rr.Body.String())
	}
	var out pb.ReedFlag
	if err := proto.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestFlagWithdrawal_LeavesTheOpenCase(t *testing.T) {
	f := newAdminFixture(t)
	frames := f.recordAdminFrames(t, "a1")
	reedID := f.localReed(t, "u2")
	flag := f.flagged(t, "u1", reedID)
	if got := decodeFlag(t, f.ownFlag(t, "u1", reedID)); !proto.Equal(got, flag) {
		t.Errorf("own flag = %+v, want the countersigned flag", got)
	}

	withdrawn := decodeFlag(t, f.withdraw(t, "u1", reedID, flag.GetSignature().GetArmor()))
	w := withdrawn.GetWithdrawal()
	sig := serverSignatureFromPB(w.GetServerSignature())
	payload := buildFlagWithdrawalServerPayload(reedID, f.ids["u1"], f.h.signingKey.Fingerprint, w.GetSignature().GetArmor(), sig.SignedAt)
	if err := f.h.services.crypto.verifySignature(context.Background(), string(payload), sig.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("withdrawal countersignature: %v", err)
	}
	if open := f.openFlags(t, "a1").Cases; len(open) != 0 {
		t.Errorf("open = %+v, want none once its only flag is withdrawn", open)
	}
	listed := f.listFlags(t, "a1", "/api/admin/flags?state=void").Cases
	if len(listed) != 1 || listed[0].VoidedAt == 0 || !proto.Equal(listed[0].Flags[0], withdrawn) {
		t.Errorf("void = %+v, want the case, void, with the withdrawn flag", listed)
	}
	if n := len(frames["a1"]); n != 2 || !proto.Equal(frames["a1"][1].GetFlag(), withdrawn) {
		t.Errorf("a1 got %d records, want the flag then its withdrawn copy", n)
	}

	if again := decodeFlag(t, f.withdraw(t, "u1", reedID, flag.GetSignature().GetArmor())); !proto.Equal(again, withdrawn) {
		t.Errorf("second withdrawal = %+v, want the one held", again)
	}
	if got := decodeFlag(t, f.ownFlag(t, "u1", reedID)); !proto.Equal(got, withdrawn) {
		t.Errorf("own flag = %+v, want it withdrawn", got)
	}
	rr := f.postFlag(t, "u1", reedID, "Again")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already-flagged") {
		t.Errorf("flagging a withdrawn flag's reed again: %d %s, want 409 already-flagged", rr.Code, rr.Body.String())
	}
}

func TestFlagWithdrawal_RefusedOnceDismissed(t *testing.T) {
	f := newAdminFixture(t)
	reedID := f.localReed(t, "u2")
	flag := f.flagged(t, "u1", reedID)
	before := f.ownFlag(t, "u1", reedID).Body.Bytes()
	if rr := f.dismiss(t, "a2", reedID, "Acted on it"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if after := f.ownFlag(t, "u1", reedID); after.Code != http.StatusOK || !slices.Equal(after.Body.Bytes(), before) {
		t.Errorf("own flag changed once dismissed: %d %q", after.Code, after.Body.String())
	}
	rr := f.withdraw(t, "u1", reedID, flag.GetSignature().GetArmor())
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "flag-acknowledged") {
		t.Errorf("withdrawing a dismissed flag: %d %s, want 409 flag-acknowledged", rr.Code, rr.Body.String())
	}
}

// A case stays open while one of its flags is; once all are withdrawn it
// is void, and the next flag opens a new case.
func TestFlagWithdrawal_LastOneVoidsTheCase(t *testing.T) {
	f := newAdminFixture(t)
	reedID := f.localReed(t, "u2")
	first := f.flagged(t, "u1", reedID)
	second := f.flagged(t, "a3", reedID)
	caseID := f.openCaseOf(t, reedID)
	if rr := f.withdraw(t, "u1", reedID, first.GetSignature().GetArmor()); rr.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", rr.Code, rr.Body.String())
	}
	if f.openCaseOf(t, reedID) != caseID {
		t.Fatal("the case closed with a flag still open")
	}
	if rr := f.withdraw(t, "a3", reedID, second.GetSignature().GetArmor()); rr.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", rr.Code, rr.Body.String())
	}
	if f.openCaseOf(t, reedID) != "" {
		t.Fatal("the case is still open with every flag withdrawn")
	}
	if rr := f.dismissCase(t, "a2", caseID, reedID, "Fine"); rr.Code != http.StatusConflict {
		t.Errorf("dismissing a void case: %d %s, want 409", rr.Code, rr.Body.String())
	}
	f.flagged(t, "root", reedID)
	if next := f.openCaseOf(t, reedID); next == "" || next == caseID {
		t.Errorf("a flag after the void case joined %q, want a new case", next)
	}
}

func TestFlagWithdrawal_OnlyTheReporter(t *testing.T) {
	f := newAdminFixture(t)
	reedID := f.localReed(t, "u2")
	flag := f.flagged(t, "u1", reedID)
	if code := f.ownFlag(t, "a3", reedID).Code; code != http.StatusNotFound {
		t.Errorf("someone else's flag: %d, want 404", code)
	}
	if code := f.withdraw(t, "a3", reedID, flag.GetSignature().GetArmor()).Code; code != http.StatusNotFound {
		t.Errorf("withdrawing without a flag: %d, want 404", code)
	}
	payload := buildFlagWithdrawalPayload(reedID, f.ids["u1"], f.keyID("u1"), "not the flag's signature")
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys["u1"].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.FlagWithdrawal{Signature: &pb.UserSignature{Id: f.keyID("u1"), Armor: sig}}
	rr := f.serve(t, f.h.WithdrawOwnFlag, http.MethodPost, "/api/flags/"+reedID+"/withdraw", "u1", msg, map[string]string{"reedID": reedID})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("a withdrawal over another signature: %d, want 401", rr.Code)
	}
}

// openFlagsRecorder keeps every value the open-flags gauge is set to.
type openFlagsRecorder struct {
	adminRecorder
	mu   sync.Mutex
	open map[string][]int
}

func (r *openFlagsRecorder) OpenFlags(_ context.Context, authorID string, open int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open[authorID] = append(r.open[authorID], open)
}

func TestFlagWithdrawal_OpenFlagsGauge(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	rec := &openFlagsRecorder{open: map[string][]int{}}
	f.h.metrics = rec
	reedID := f.localReed(t, "u2")
	flags := map[string]*pb.ReedFlag{}
	for _, reporter := range []string{"a3", "root", "u1"} {
		flags[reporter] = f.flagged(t, reporter, reedID)
	}
	if rr := f.withdraw(t, "a3", reedID, flags["a3"].GetSignature().GetArmor()); rr.Code != http.StatusOK {
		t.Fatalf("withdraw: %d", rr.Code)
	}
	if rr := f.dismiss(t, "a2", reedID, "Fine"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	f.kick(t, "u1")
	if got, want := rec.open[f.ids["u2"]], []int{1, 2, 3, 2, 0}; !slices.Equal(got, want) {
		t.Errorf("u2's open flags went %v, want %v", got, want)
	}
}

// A withdrawal of, and a lookup of, a flag on a peer's reed go to that
// peer, and its answer comes back the same way.
func TestFlagWithdrawal_RelayedToTheReedsServer(t *testing.T) {
	f := newAdminFixture(t)
	const peerID = "peer5678"
	reedID := string(canonicalID(peerID, "zed")) + "/r1"
	var mu sync.Mutex
	var calls []string
	var bodies []*pb.ReedFlag
	held := &pb.ReedFlag{ReedId: reedID, ReporterId: f.ids["u1"], Note: "Spam"}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var msg pb.ReedFlag
		_ = proto.Unmarshal(raw, &msg)
		mu.Lock()
		calls, bodies = append(calls, r.URL.Path), append(bodies, &msg)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/flag-withdrawal") {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, "flag-acknowledged")
			return
		}
		out, _ := proto.Marshal(held)
		w.Header().Set("Content-Type", protobufContentType)
		_, _ = w.Write(out)
	}))
	t.Cleanup(peer.Close)
	f.h.federationHTTPClientOverride = peer.Client()
	if _, err := f.db.Exec(`INSERT INTO servers (id, name, base_url, frontend_url) VALUES ($1, $1, $2, $2)`, peerID, peer.URL); err != nil {
		t.Fatal(err)
	}
	peerTestMesh(t, f.db, peerID)

	if got := decodeFlag(t, f.ownFlag(t, "u1", reedID)); !proto.Equal(got, held) {
		t.Errorf("relayed lookup = %+v, want the peer's answer", got)
	}
	rr := f.withdraw(t, "u1", reedID, "FLAGSIG")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "flag-acknowledged") {
		t.Errorf("relayed refusal: %d %s, want 409 flag-acknowledged", rr.Code, rr.Body.String())
	}
	want := []string{"/api/federation/relay/flag-lookup", "/api/federation/relay/flag-withdrawal"}
	if !slices.Equal(calls, want) {
		t.Fatalf("peer calls = %v, want %v", calls, want)
	}
	if b := bodies[1]; b.GetReedId() != reedID || b.GetReporterId() != f.ids["u1"] || b.GetWithdrawal().GetSignature().GetId() != f.keyID("u1") {
		t.Errorf("relayed withdrawal = %+v, want u1's signed withdrawal", b)
	}
}

// withdraw has u withdraw their flag and returns it withdrawn.
func (s *e2eServer) withdraw(t *testing.T, u *e2eUser, flag *pb.ReedFlag) *pb.ReedFlag {
	t.Helper()
	reedID := flag.GetReedId()
	sig := s.sign(t, u, buildFlagWithdrawalPayload(reedID, u.id, u.keyID, flag.GetSignature().GetArmor()))
	msg := &pb.FlagWithdrawal{Signature: &pb.UserSignature{Id: u.keyID, Armor: sig}}
	rr := s.authed(t, u, http.MethodPost, "/api/flags/"+reedID+"/withdraw", msg, map[string]string{"reedID": reedID}, s.h.WithdrawOwnFlag)
	expectStatus(t, "withdraw", rr, http.StatusOK)
	var out pb.ReedFlag
	decodeProto(t, rr.Body.Bytes(), &out)
	return &out
}

// A withdrawn copy reported after an unwithdrawn one withdraws the flag;
// the reverse leaves it withdrawn.
func TestRecoveryE2E_WithdrawnFlagCopies(t *testing.T) {
	sc := newRecoveryScenario(t)
	open := sc.flags[0]
	before := map[*e2eUser]device{}
	for _, u := range sc.everyone {
		before[u] = sc.deviceOf(t, u)
	}
	withdrawn := sc.a.withdraw(t, sc.bob, open)
	sc.flags[0] = withdrawn
	withdrawnAt := func(s *e2eServer) string {
		t.Helper()
		return queryRows(t, s.db, `SELECT COALESCE(ss.signed_at::text, 'open') FROM reed_flags f
			LEFT JOIN server_signatures ss ON ss.id = f.withdrawal_server_signature_id
			WHERE f.reed_id = '`+open.GetReedId()+`' AND f.reporter_id = '`+sc.bob.id+`'`)[0]
	}
	want := withdrawnAt(sc.a)

	t.Run("withdrawn after open", func(t *testing.T) {
		b := restoreIdentity(t, sc.a)
		for _, u := range sc.everyone {
			b.recover(t, before[u])
		}
		if got := withdrawnAt(b); got != "open" {
			t.Fatalf("restored from open copies: %s, want open", got)
		}
		b.reportRecords(t, sc.bob, slices.All([]*pb.RecoveryRecord{{Record: &pb.RecoveryRecord_Flag{Flag: withdrawn}}}))
		if got := withdrawnAt(b); got != want {
			t.Errorf("after the withdrawn copy: %s, want withdrawn at %s", got, want)
		}
	})

	t.Run("open after withdrawn", func(t *testing.T) {
		b := restoreIdentity(t, sc.a)
		for _, u := range sc.everyone {
			b.recover(t, sc.deviceOf(t, u))
		}
		if got := withdrawnAt(b); got != want {
			t.Fatalf("restored from withdrawn copies: %s, want withdrawn at %s", got, want)
		}
		b.reportRecords(t, sc.bob, slices.All([]*pb.RecoveryRecord{{Record: &pb.RecoveryRecord_Flag{Flag: open}}}))
		if got := withdrawnAt(b); got != want {
			t.Errorf("an open copy reopened it: %s", got)
		}
	})
}
