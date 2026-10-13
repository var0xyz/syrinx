//go:build !ops

package main

import (
	"net/http"
	"testing"
	"time"

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
	createdAt := time.Now().UTC().Truncate(time.Second)
	payload := buildReedFlagPayload(reedID, f.ids[reporter], f.keyID(reporter), note, createdAt)
	sig, err := f.h.services.crypto.sign(string(payload), f.keys[reporter].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.ReedFlag{ReedId: reedID, ReporterId: f.ids[reporter], Note: note, CreatedAt: unixOrZero(createdAt),
		Signature: &pb.UserSignature{Id: f.keyID(reporter), Armor: sig}}
	return f.serve(t, f.h.FlagReed, http.MethodPost, "/api/flags", reporter, msg, nil).Code
}

func (f *adminFixture) openFlags(t *testing.T, viewer string) *pb.FlaggedReedList {
	t.Helper()
	rr := f.serve(t, f.h.ListFlags, http.MethodGet, "/api/admin/flags", viewer, nil, nil)
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

	if code := f.serve(t, f.h.DismissFlags, http.MethodPost, "/api/admin/flags/"+adminReed+"/dismiss", "a1", nil,
		map[string]string{"reedID": adminReed}).Code; code != http.StatusNotFound {
		t.Errorf("an admin dismissing flags on their own reed: %d, want 404", code)
	}
	if code := f.serve(t, f.h.DismissFlags, http.MethodPost, "/api/admin/flags/"+userReed+"/dismiss", "a2", nil,
		map[string]string{"reedID": userReed}).Code; code != http.StatusNoContent {
		t.Fatalf("dismiss: %d", code)
	}
	if got := seen(f.openFlags(t, "a2")); got[userReed] {
		t.Errorf("a dismissed reed is still open: %v", got)
	}
}

func TestFlags_RemovedReporterLeftOut(t *testing.T) {
	f := newAdminFixture(t)
	reed := f.localReed(t, "u2")
	if code := f.flag(t, "u1", reed, ""); code != http.StatusCreated {
		t.Fatal(code)
	}
	if _, err := f.db.Exec(`
		WITH us AS (INSERT INTO user_signatures (public_key_id, signature) VALUES ($2, 's') RETURNING id),
		     ss AS (INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id)
		INSERT INTO account_removals (user_id, public_key_id, user_signature_id, server_signature_id)
		SELECT $1, $2, us.id, ss.id FROM us, ss
	`, f.ids["u1"], f.keyID("u1")); err != nil {
		t.Fatal(err)
	}
	if list := f.openFlags(t, "a2"); len(list.Reeds) != 0 {
		t.Fatalf("flags = %+v, want none from a removed reporter", list.Reeds)
	}
}
