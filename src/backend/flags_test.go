//go:build !ops

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// localReed stores a reed identity authored by author and returns its ID.
func (f *adminFixture) localReed(t *testing.T, author string) string {
	t.Helper()
	id := f.ids[author] + "/" + uuid.NewString()
	if _, err := f.db.Exec(`INSERT INTO reed_identities (id, server_id, author_id) VALUES ($1, $2, $3)`,
		id, f.srv, f.ids[author]); err != nil {
		t.Fatal(err)
	}
	return id
}

// flag has reporter sign and post a flag on reedID.
func (f *adminFixture) flag(t *testing.T, reporter, reedID, note string) int {
	t.Helper()
	return f.postFlag(t, reporter, reedID, note).Code
}

func (f *adminFixture) postFlag(t *testing.T, reporter, reedID, note string) *httptest.ResponseRecorder {
	t.Helper()
	payload := buildReedFlagPayload(reedID, f.ids[reporter], f.keyID(reporter), note)
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[reporter].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.ReedFlag{ReedId: reedID, ReporterId: f.ids[reporter], Note: note,
		Signature: &pb.UserSignature{Id: f.keyID(reporter), Armor: sig}}
	return f.serve(t, f.h.FlagReed, http.MethodPost, "/api/flags", reporter, msg, nil)
}

func (f *adminFixture) openFlags(t *testing.T, viewer string) *pb.FlagCaseList {
	t.Helper()
	return f.listFlags(t, viewer, "/api/admin/flags")
}

func (f *adminFixture) dismissedFlags(t *testing.T, viewer string) *pb.FlagCaseList {
	t.Helper()
	return f.listFlags(t, viewer, "/api/admin/flags?state=dismissed")
}

// openCaseOf is reedID's open case, or "".
func (f *adminFixture) openCaseOf(t *testing.T, reedID string) string {
	t.Helper()
	var id string
	err := f.db.QueryRow(`SELECT id FROM flag_cases WHERE reed_id = $1 AND dismissed_at IS NULL AND voided_at IS NULL`, reedID).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return id
}

// dismiss has viewer sign and post a dismissal of reedID's open case.
func (f *adminFixture) dismiss(t *testing.T, viewer, reedID, reason string) *httptest.ResponseRecorder {
	t.Helper()
	caseID := f.openCaseOf(t, reedID)
	if caseID == "" {
		caseID = uuid.NewString()
	}
	return f.dismissCase(t, viewer, caseID, reedID, reason)
}

func (f *adminFixture) dismissCase(t *testing.T, viewer, caseID, reedID, reason string) *httptest.ResponseRecorder {
	t.Helper()
	payload := buildFlagDismissalPayload(caseID, reedID, strings.TrimSpace(reason), f.keyID(viewer))
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[viewer].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.DismissCaseRequest{Reason: reason, Signature: &pb.UserSignature{Id: f.keyID(viewer), Armor: sig}}
	return f.serve(t, f.h.DismissCase, http.MethodPost, "/api/admin/flags/cases/"+caseID+"/dismiss", viewer,
		msg, map[string]string{"caseID": caseID})
}

func (f *adminFixture) listFlags(t *testing.T, viewer, path string) *pb.FlagCaseList {
	t.Helper()
	rr := f.serve(t, f.h.ListFlags, http.MethodGet, path, viewer, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list flags as %s: %d %s", viewer, rr.Code, rr.Body.String())
	}
	var list pb.FlagCaseList
	if err := proto.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return &list
}

func TestFlags_RefusedFlags(t *testing.T) {
	f := newAdminFixture(t)
	own := f.localReed(t, "u1")
	rootReed := f.localReed(t, "root")
	other := f.localReed(t, "u2")

	if code := f.flag(t, "u1", own, ""); code != http.StatusForbidden {
		t.Errorf("flag on own reed: %d, want 403", code)
	}
	if code := f.flag(t, "u1", rootReed, ""); code != http.StatusForbidden {
		t.Errorf("flag on root's reed: %d, want 403", code)
	}
	if code := f.flag(t, "u1", other, "Scam link"); code != http.StatusCreated {
		t.Fatalf("flag: %d", code)
	}
	if code := f.flag(t, "u1", other, "Again"); code != http.StatusConflict {
		t.Errorf("second flag by the same reporter: %d, want 409", code)
	}
	if code := f.flag(t, "u1", f.ids["u2"]+"/missing", ""); code != http.StatusNotFound {
		t.Errorf("flag on an unknown reed: %d, want 404", code)
	}
}

func TestFlags_CountersignedOnArrival(t *testing.T) {
	f := newAdminFixture(t)
	reedID := f.localReed(t, "u2")
	rr := f.postFlag(t, "u1", reedID, "Scam link")
	if rr.Code != http.StatusCreated {
		t.Fatalf("flag: %d", rr.Code)
	}
	var got pb.ReedFlag
	if err := proto.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	sig := serverSignatureFromPB(got.GetServerSignature())
	if got.GetCaseId() == "" || got.GetCaseId() != f.openCaseOf(t, reedID) {
		t.Fatalf("flag case = %q, want the reed's open case", got.GetCaseId())
	}
	payload := buildReedFlagServerPayload(reedID, f.ids["u1"], got.GetCaseId(), f.h.signingKey.Fingerprint, got.GetSignature().GetArmor(), sig.SignedAt)
	if err := f.h.services.crypto.verifySignature(context.Background(), string(payload), sig.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("countersignature: %v", err)
	}
	other := buildReedFlagServerPayload(reedID, f.ids["u1"], uuid.NewString(), f.h.signingKey.Fingerprint, got.GetSignature().GetArmor(), sig.SignedAt)
	if err := f.h.services.crypto.verifySignature(context.Background(), string(other), sig.Armor, f.h.signingKey.Armor); err == nil {
		t.Fatal("the countersignature verifies for another case")
	}

	listed := f.openFlags(t, "a1").Cases
	if len(listed) != 1 || len(listed[0].Flags) != 1 || !proto.Equal(listed[0].Flags[0].GetServerSignature(), got.GetServerSignature()) {
		t.Errorf("listed flags = %+v, want the countersigned flag", listed)
	}
}

func TestFlags_VisibilityAndDismiss(t *testing.T) {
	f := newAdminFixture(t)
	userReed := f.localReed(t, "u2")
	adminReed := f.localReed(t, "a1")
	if code := f.flag(t, "u1", userReed, "Scam link"); code != http.StatusCreated {
		t.Fatal(code)
	}
	if code := f.flag(t, "u1", adminReed, "Rude"); code != http.StatusCreated {
		t.Fatal(code)
	}

	seen := func(list *pb.FlagCaseList) map[string]bool {
		out := map[string]bool{}
		for _, c := range list.Cases {
			out[c.ReedId] = true
		}
		return out
	}
	if got := seen(f.openFlags(t, "a1")); !got[userReed] || got[adminReed] {
		t.Errorf("a1 sees %v, want the user's reed but not their own", got)
	}
	a2 := f.openFlags(t, "a2")
	if got := seen(a2); !got[userReed] || !got[adminReed] {
		t.Errorf("a2 sees %v, want both", got)
	}
	for _, c := range a2.Cases {
		if c.ReedId == userReed && (len(c.Flags) != 1 || c.Flags[0].Note != "Scam link" || c.Flags[0].ReporterId != f.ids["u1"]) {
			t.Errorf("flags on the user's reed = %+v", c.Flags)
		}
	}
	if code := f.serve(t, f.h.ListFlags, http.MethodGet, "/api/admin/flags", "u2", nil, nil).Code; code != http.StatusForbidden {
		t.Errorf("a member listing flags: %d, want 403", code)
	}

	if code := f.dismiss(t, "a1", adminReed, "Fine").Code; code != http.StatusNotFound {
		t.Errorf("an admin dismissing a case on their own reed: %d, want 404", code)
	}
	if code := f.dismiss(t, "a2", userReed, "  ").Code; code != http.StatusBadRequest {
		t.Errorf("dismiss without a reason: %d, want 400", code)
	}
	if code := f.dismiss(t, "a2", userReed, strings.Repeat("x", maxDismissalReasonChars+1)).Code; code != http.StatusBadRequest {
		t.Errorf("dismiss with an overlong reason: %d, want 400", code)
	}
	firstCase := f.openCaseOf(t, userReed)
	if rr := f.dismiss(t, "a2", userReed, "Not a scam"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if got := seen(f.openFlags(t, "a2")); got[userReed] {
		t.Errorf("a dismissed case is still open: %v", got)
	}
	if rr := f.dismissCase(t, "a2", firstCase, userReed, "Again"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "case-closed") {
		t.Errorf("dismissing a dismissed case: %d %s, want 409 case-closed", rr.Code, rr.Body.String())
	}

	dismissed := f.dismissedFlags(t, "a1").Cases
	if len(dismissed) != 1 || dismissed[0].ReedId != userReed || dismissed[0].Id != firstCase {
		t.Fatalf("dismissed = %+v, want the user's reed's case", dismissed)
	}
	d := dismissed[0]
	if d.Dismissal.GetReason() != "Not a scam" || d.Dismissal.GetDismissedBy() != f.ids["a2"] || d.Dismissal.GetServerSignature().GetSignedAt() == 0 {
		t.Errorf("dismissal = %+v", d.Dismissal)
	}
	if len(d.Flags) != 1 || d.Flags[0].Note != "Scam link" {
		t.Errorf("dismissed = %+v, want the closed flag", d)
	}
	if entries := f.ledger(t); len(entries) != 0 {
		t.Errorf("ledger = %+v, want no entry for a dismissal", entries)
	}

	if code := f.flag(t, "a3", userReed, "Still spam"); code != http.StatusCreated {
		t.Fatalf("flag after a dismissal: %d", code)
	}
	if second := f.openCaseOf(t, userReed); second == "" || second == firstCase {
		t.Fatalf("a flag after the dismissal joined case %q, want a new one", second)
	}
	if rr := f.dismiss(t, "a1", userReed, "Still fine"); rr.Code != http.StatusOK {
		t.Fatalf("second dismiss: %d", rr.Code)
	}
	dismissed = f.dismissedFlags(t, "a2").Cases
	if len(dismissed) != 2 || dismissed[0].Dismissal.GetReason() != "Still fine" ||
		len(dismissed[0].Flags) != 1 || dismissed[0].Flags[0].Note != "Still spam" {
		t.Errorf("dismissed = %+v, want the second case first with only its own flag", dismissed)
	}
}

// A flag raised while a case is open joins it, newest first, and is
// dismissed with it even if the admin never saw it.
func TestFlags_LateFlagJoinsTheOpenCase(t *testing.T) {
	f := newAdminFixture(t)
	reed := f.localReed(t, "u2")
	for _, reporter := range []string{"u1", "a3"} {
		if code := f.flag(t, reporter, reed, reporter); code != http.StatusCreated {
			t.Fatal(code)
		}
	}
	open := f.openFlags(t, "a2").Cases
	if len(open) != 1 || len(open[0].Flags) != 2 || open[0].Flags[0].Note != "a3" {
		t.Fatalf("open = %+v, want one case, its newest flag first", open)
	}
	if rr := f.dismiss(t, "a2", reed, "Fine"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if n := len(f.openFlags(t, "a2").Cases); n != 0 {
		t.Errorf("%d cases open after the dismissal", n)
	}
}

// Two first flags at once end up in one case.
func TestFlags_ConcurrentFirstFlagsShareACase(t *testing.T) {
	f := newAdminFixture(t)
	reed := f.localReed(t, "u2")
	var wg sync.WaitGroup
	for _, reporter := range []string{"u1", "a3", "root"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code := f.flag(t, reporter, reed, ""); code != http.StatusCreated {
				t.Errorf("%s flag: %d", reporter, code)
			}
		}()
	}
	wg.Wait()
	if n := queryRows(t, f.db, `SELECT COUNT(DISTINCT case_id) FROM reed_flags WHERE reed_id = '`+reed+`'`); n[0] != "1" {
		t.Errorf("flags in %s cases, want 1", n[0])
	}
}

// A removed reporter's flags in open cases go: a case left empty goes, one
// left with only withdrawn flags is void. Dismissed cases stay whole.
func TestFlags_RemovedReporterFlagsInOpenCasesDeleted(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	alone, withdrawn, dismissed := f.localReed(t, "u2"), f.localReed(t, "a3"), f.localReed(t, "a1")
	for _, flag := range []struct{ reporter, reed string }{{"u1", alone}, {"u1", withdrawn}, {"root", withdrawn}, {"u1", dismissed}} {
		if code := f.flag(t, flag.reporter, flag.reed, ""); code != http.StatusCreated {
			t.Fatal(code)
		}
	}
	rootFlag := decodeFlag(t, f.ownFlag(t, "root", withdrawn))
	if rr := f.withdraw(t, "root", withdrawn, rootFlag.GetSignature().GetArmor()); rr.Code != http.StatusOK {
		t.Fatalf("withdraw: %d", rr.Code)
	}
	if rr := f.dismiss(t, "a2", dismissed, "Fine"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}

	f.kick(t, "u1")
	if n := queryRows(t, f.db, `SELECT COUNT(*) FROM flag_cases WHERE reed_id = '`+alone+`'`); n[0] != "0" {
		t.Errorf("a case left empty kept: %s", n[0])
	}
	if v := queryRows(t, f.db, `SELECT voided_at IS NOT NULL FROM flag_cases WHERE reed_id = '`+withdrawn+`'`); len(v) != 1 || v[0] != "true" {
		t.Errorf("a case left with only withdrawn flags: void = %v, want true", v)
	}
	kept := f.dismissedFlags(t, "a3").Cases
	if len(kept) != 1 || kept[0].ReedId != dismissed || len(kept[0].Flags) != 1 || kept[0].Flags[0].ReporterId != f.ids["u1"] {
		t.Errorf("dismissed = %+v, want the case with the removed reporter's flag", kept)
	}
}

// A case closed less than six months ago can't be deleted, nor an open
// one; past that it goes with its flags and their signatures.
func TestFlags_DeleteOldClosedCase(t *testing.T) {
	f := newAdminFixture(t)
	frames := f.recordAdminFrames(t, "a1")
	reed := f.localReed(t, "u2")
	if code := f.flag(t, "u1", reed, ""); code != http.StatusCreated {
		t.Fatal(code)
	}
	caseID := f.openCaseOf(t, reed)
	del := func() *httptest.ResponseRecorder {
		return f.serve(t, f.h.DeleteFlagCase, http.MethodDelete, "/api/admin/flags/cases/"+caseID, "a2", nil, map[string]string{"caseID": caseID})
	}
	if rr := del(); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "case-open") {
		t.Errorf("deleting an open case: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.dismiss(t, "a2", reed, "Fine"); rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	if rr := del(); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "case-too-recent") {
		t.Errorf("deleting a fresh case: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := f.db.Exec(`UPDATE flag_cases SET dismissed_at = NOW() - INTERVAL '200 days' WHERE id = $1`, caseID); err != nil {
		t.Fatal(err)
	}
	sigs := queryRows(t, f.db, `SELECT COUNT(*) FROM server_signatures`)
	if rr := del(); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if n := queryRows(t, f.db, `SELECT COUNT(*) FROM reed_flags WHERE reed_id = '`+reed+`'`); n[0] != "0" {
		t.Errorf("flags left: %s", n[0])
	}
	if after := queryRows(t, f.db, `SELECT COUNT(*) FROM server_signatures`); after[0] == sigs[0] {
		t.Error("the case's signatures were kept")
	}
	last := frames["a1"][len(frames["a1"])-1]
	if last.GetDeletedCaseId() != caseID {
		t.Errorf("a1's last record = %+v, want the deleted case", last)
	}
}
