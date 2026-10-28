//go:build !ops

package main

import (
	"net/http"
	"testing"
	"time"

	pb "syrinx/proto"
)

// createInvite has name mint one plain invite, returning the status.
func (f *adminFixture) createInvite(t *testing.T, name string) int {
	t.Helper()
	body := inviteCreateBody(t, f.h, f.ids[name], f.keys[name], "", "", time.Now(), "")
	return postCreateInvite(f.h, f.ids[name], body).Code
}

func (f *adminFixture) inviteLimit(t *testing.T, name string) int32 {
	t.Helper()
	rr := f.serve(t, f.h.GetInviteQuota, http.MethodGet, "/api/invites/quota", name, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("quota: %d %s", rr.Code, rr.Body.String())
	}
	var quota pb.InviteQuota
	decodeProto(t, rr.Body.Bytes(), &quota)
	return quota.Limit
}

func TestCreateInvite_RootHasNoLimit(t *testing.T) {
	f := newAdminFixture(t)
	f.h.cfg.MaxInvitesPerUser = 1
	for i := 0; i < 3; i++ {
		if code := f.createInvite(t, "root"); code != http.StatusCreated {
			t.Fatalf("root invite %d: %d", i, code)
		}
	}
	if got := f.inviteLimit(t, "root"); got != maxInvitesUnlimited {
		t.Errorf("root limit = %d, want unlimited", got)
	}
	if code := f.createInvite(t, "u1"); code != http.StatusCreated {
		t.Fatalf("u1 first invite: %d", code)
	}
	if code := f.createInvite(t, "u1"); code != http.StatusForbidden {
		t.Fatalf("u1 over the cap: %d, want 403", code)
	}
}

func TestInvitesGrant_RaisesLimitAndIsLogged(t *testing.T) {
	f := newAdminFixture(t)
	f.h.cfg.MaxInvitesPerUser = 1
	f.grant(t, "a1", permUserInvites)
	f.grant(t, "a2", permUserInvites)
	if code := f.createInvite(t, "u1"); code != http.StatusCreated {
		t.Fatalf("first invite: %d", code)
	}
	if code := f.createInvite(t, "u1"); code != http.StatusForbidden {
		t.Fatalf("over the cap: %d, want 403", code)
	}

	msg := f.request(t, "a1", adminActionInvites, "Bringing in their team", "2", "u1")
	f.openAs(t, "a1", msg)
	if got := f.inviteLimit(t, "u1"); got != 1 {
		t.Errorf("limit before approval = %d, want 1", got)
	}
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	if got := f.inviteLimit(t, "u1"); got != 3 {
		t.Errorf("limit after the grant = %d, want 3", got)
	}
	for i := 0; i < 2; i++ {
		if code := f.createInvite(t, "u1"); code != http.StatusCreated {
			t.Fatalf("granted invite %d: %d", i, code)
		}
	}
	if code := f.createInvite(t, "u1"); code != http.StatusForbidden {
		t.Fatalf("over the raised cap: %d, want 403", code)
	}
	if got := f.inviteLimit(t, "u2"); got != 1 {
		t.Errorf("u2 limit = %d, want the server cap", got)
	}

	var found bool
	for _, e := range f.ledger(t) {
		if e.Message == "["+msg.Id+"] Invite grant: 2 more. Reason: Bringing in their team" {
			found = true
		}
	}
	if !found {
		t.Errorf("no ledger entry for the grant: %+v", f.ledger(t))
	}
}

func TestInvitesGrant_Refusals(t *testing.T) {
	f := newAdminFixture(t)
	f.h.cfg.MaxInvitesPerUser = 1
	f.grant(t, "a1", permUserInvites)
	open := func(requester, body string, targets ...string) int {
		msg := f.request(t, requester, adminActionInvites, "More invites", body, targets...)
		return f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", requester, msg, nil).Code
	}
	for _, body := range []string{"", "0", "02", "-1", "abc", "101"} {
		if code := open("a1", body, "u1"); code != http.StatusBadRequest {
			t.Errorf("body %q: %d, want 400", body, code)
		}
	}
	if code := open("a1", "1", "u1", "u2"); code != http.StatusBadRequest {
		t.Errorf("two targets: %d, want 400", code)
	}
	if code := open("a2", "1", "u1"); code != http.StatusForbidden {
		t.Errorf("without user.invites: %d, want 403", code)
	}
	f.h.cfg.MaxInvitesPerUser = maxInvitesUnlimited
	if code := open("a1", "1", "u1"); code != http.StatusConflict {
		t.Errorf("no cap on the server: %d, want 409", code)
	}
}
