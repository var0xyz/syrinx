//go:build !ops

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

func (f *adminFixture) openFlags(t *testing.T, viewer string) *pb.FlaggedReedList {
	t.Helper()
	return f.listFlags(t, viewer, "/api/admin/flags")
}

func (f *adminFixture) dismissedFlags(t *testing.T, viewer string) *pb.FlaggedReedList {
	t.Helper()
	return f.listFlags(t, viewer, "/api/admin/flags?state=dismissed")
}

// dismiss has viewer sign and post a dismissal of the open flags on
// reedID, or only reporters' when given.
func (f *adminFixture) dismiss(t *testing.T, viewer, reedID, reason string, reporters ...string) *httptest.ResponseRecorder {
	t.Helper()
	if len(reporters) == 0 {
		rows, err := f.db.Query(`SELECT reporter_id FROM reed_flags WHERE reed_id = $1 AND dismissal_id IS NULL AND withdrawal_signature_id IS NULL ORDER BY reporter_id`, reedID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			reporters = append(reporters, id)
		}
		rows.Close()
	}
	slices.Sort(reporters)
	id := uuid.Must(uuid.NewV7()).String()
	payload := buildFlagDismissalPayload(id, reedID, strings.TrimSpace(reason), f.keyID(viewer), reporters)
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[viewer].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.DismissFlagsRequest{Id: id, Reason: reason, Reporters: reporters,
		Signature: &pb.UserSignature{Id: f.keyID(viewer), Armor: sig}}
	return f.serve(t, f.h.DismissFlags, http.MethodPost, "/api/admin/flags/"+reedID+"/dismiss", viewer,
		msg, map[string]string{"reedID": reedID})
}

func (f *adminFixture) listFlags(t *testing.T, viewer, path string) *pb.FlaggedReedList {
	t.Helper()
	rr := f.serve(t, f.h.ListFlags, http.MethodGet, path, viewer, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list flags as %s: %d %s", viewer, rr.Code, rr.Body.String())
	}
	var list pb.FlaggedReedList
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
	payload := buildReedFlagServerPayload(reedID, f.ids["u1"], f.h.signingKey.Fingerprint, got.GetSignature().GetArmor(), sig.SignedAt)
	if err := f.h.services.crypto.verifySignature(context.Background(), string(payload), sig.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("countersignature: %v", err)
	}

	listed := f.openFlags(t, "a1").Reeds
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

	seen := func(list *pb.FlaggedReedList) map[string]bool {
		out := map[string]bool{}
		for _, r := range list.Reeds {
			out[r.ReedId] = true
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
	for _, r := range a2.Reeds {
		if r.ReedId == userReed && (len(r.Flags) != 1 || r.Flags[0].Note != "Scam link" || r.Flags[0].ReporterId != f.ids["u1"]) {
			t.Errorf("flags on the user's reed = %+v", r.Flags)
		}
	}
	if code := f.serve(t, f.h.ListFlags, http.MethodGet, "/api/admin/flags", "u2", nil, nil).Code; code != http.StatusForbidden {
		t.Errorf("a member listing flags: %d, want 403", code)
	}

	if code := f.dismiss(t, "a1", adminReed, "Fine").Code; code != http.StatusNotFound {
		t.Errorf("an admin dismissing flags on their own reed: %d, want 404", code)
	}
	if code := f.dismiss(t, "a2", userReed, "  ").Code; code != http.StatusBadRequest {
		t.Errorf("dismiss without a reason: %d, want 400", code)
	}
	if code := f.dismiss(t, "a2", userReed, strings.Repeat("x", maxDismissalReasonChars+1)).Code; code != http.StatusBadRequest {
		t.Errorf("dismiss with an overlong reason: %d, want 400", code)
	}
	if rr := f.dismiss(t, "a2", userReed, "Not a scam"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if got := seen(f.openFlags(t, "a2")); got[userReed] {
		t.Errorf("a dismissed reed is still open: %v", got)
	}
	if code := f.dismiss(t, "a2", userReed, "Again", f.ids["u1"]).Code; code != http.StatusNotFound {
		t.Errorf("dismissing an already dismissed reed: %d, want 404", code)
	}

	dismissed := f.dismissedFlags(t, "a1").Reeds
	if len(dismissed) != 1 || dismissed[0].ReedId != userReed {
		t.Fatalf("dismissed = %+v, want the user's reed", dismissed)
	}
	d := dismissed[0]
	if d.Dismissal.GetReason() != "Not a scam" || d.Dismissal.GetDismissedBy() != f.ids["a2"] || d.Dismissal.GetServerSignature().GetSignedAt() == 0 {
		t.Errorf("dismissal = %+v", d.Dismissal)
	}
	if len(d.Flags) != 1 || d.Flags[0].Note != "Scam link" || !slices.Equal(d.Dismissal.GetReporters(), []string{f.ids["u1"]}) {
		t.Errorf("dismissed = %+v, want the closed flag and its reporter", d)
	}
	if entries := f.ledger(t); len(entries) != 0 {
		t.Errorf("ledger = %+v, want no entry for a dismissal", entries)
	}

	if code := f.flag(t, "a3", userReed, "Still spam"); code != http.StatusCreated {
		t.Fatalf("flag after a dismissal: %d", code)
	}
	if rr := f.dismiss(t, "a1", userReed, "Still fine"); rr.Code != http.StatusOK {
		t.Fatalf("second dismiss: %d", rr.Code)
	}
	dismissed = f.dismissedFlags(t, "a2").Reeds
	if len(dismissed) != 2 || dismissed[0].Dismissal.GetReason() != "Still fine" ||
		len(dismissed[0].Flags) != 1 || dismissed[0].Flags[0].Note != "Still spam" {
		t.Errorf("dismissed = %+v, want the second dismissal first with only its own flag", dismissed)
	}
}

// A removed reporter's flags go, and a dismissal left with none of its
// flags goes with them; one keeping others stays.
func TestFlags_RemovedReporterFlagsDeleted(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	alone, shared := f.localReed(t, "u2"), f.localReed(t, "a3")
	for _, flag := range []struct{ reporter, reed string }{{"u1", alone}, {"u1", shared}, {"root", shared}} {
		if code := f.flag(t, flag.reporter, flag.reed, ""); code != http.StatusCreated {
			t.Fatal(code)
		}
	}
	if rr := f.dismiss(t, "a2", alone, "Fine"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.dismiss(t, "a2", shared, "Fine too"); rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}

	f.kick(t, "u1")
	if n := queryRows(t, f.db, `SELECT COUNT(*) FROM reed_flags WHERE reporter_id = '`+f.ids["u1"]+`'`); n[0] != "0" {
		t.Errorf("flags left by the kicked reporter: %s", n[0])
	}
	dismissed := f.dismissedFlags(t, "a1").Reeds
	if len(dismissed) != 1 || dismissed[0].ReedId != shared || len(dismissed[0].Flags) != 1 ||
		!slices.Equal(dismissed[0].Dismissal.GetReporters(), []string{f.ids["root"], f.ids["u1"]}) {
		t.Errorf("dismissed = %+v, want only the shared reed's, keeping its signed reporters", dismissed)
	}
}
