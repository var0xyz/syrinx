//go:build !ops

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// profileSignedAt is when userID's profile was last countersigned.
func (f *adminFixture) profileSignedAt(t *testing.T, userID string) time.Time {
	t.Helper()
	var signedAt time.Time
	if err := f.db.QueryRow(`
		SELECT ss.signed_at FROM users u JOIN server_signatures ss ON ss.id = u.server_signature_id WHERE u.id = $1
	`, userID).Scan(&signedAt); err != nil {
		t.Fatal(err)
	}
	return signedAt
}

func (f *adminFixture) permissions(t *testing.T, name string) []string {
	t.Helper()
	perms, err := f.ds.GetUserPermissions(context.Background(), f.ids[name])
	if err != nil {
		t.Fatal(err)
	}
	return perms
}

func TestPromote_GrantsRolePermissionsAndFreshProfile(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionPromote)
	f.grant(t, "a1", permAdminCreate)
	f.grant(t, "a2", permAdminCreate)
	before := f.profileSignedAt(t, f.ids["u1"])
	time.Sleep(1100 * time.Millisecond)

	msg := f.request(t, "a1", adminActionPromote, "Helps everyone", permUserKick, "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve promote: %d %s", rr.Code, rr.Body.String())
	}
	if role := queryUserRole(t, f.db, f.ids["u1"]); role != roleAdmin {
		t.Fatalf("role = %s, want admin", role)
	}
	if got := f.permissions(t, "u1"); !slices.Equal(got, []string{permUserKick}) {
		t.Fatalf("permissions = %v, want user.kick", got)
	}
	if after := f.profileSignedAt(t, f.ids["u1"]); !after.After(before) {
		t.Fatalf("profile countersigned at %v, want after %v", after, before)
	}
	profile, err := f.ds.GetUserProfile(context.Background(), f.ids["u1"])
	if err != nil || profile == nil {
		t.Fatal(err)
	}
	inviteID := ""
	if profile.Invite != nil {
		inviteID = profile.Invite.ID
	}
	if !slices.Equal(profile.Permissions, []string{permUserKick}) {
		t.Fatalf("profile permissions = %v, want user.kick", profile.Permissions)
	}
	payload := buildProfilePayload(f.ids["u1"], profile.Username, profile.UserSignature.ID, f.srv, f.h.signingKey.Fingerprint,
		profile.UserSignature.Armor, inviteID, roleAdmin, []string{permUserKick}, profile.Bio, profile.CreatedAt, profile.ServerSignature.SignedAt)
	if err := f.h.services.crypto.verifySignature(context.Background(), string(payload), profile.ServerSignature.Armor, f.h.signingKey.Armor); err != nil {
		t.Fatalf("re-countersigned profile doesn't verify with role admin: %v", err)
	}
}

func TestPromote_ApproverMustHoldGrantedPermissions(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionPromote)
	f.grant(t, "a1", permAdminCreate, permUserRebind)
	f.grant(t, "a2", permAdminCreate)

	msg := f.request(t, "a1", adminActionPromote, "Helps", permUserRebind, "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Fatalf("approver without user.rebind: %d, want 403", rr.Code)
	}
}

func TestPermissions_AddRemoveAndRootApproval(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionPermissions)
	f.grant(t, "a1", permAdminCreate, permAdminDemote)
	f.grant(t, "a2", permAdminCreate, permAdminDemote)

	msg := f.request(t, "a1", adminActionPermissions, "Only suspensions", permUserSuspend, "a3")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Fatalf("admin approving a change to an admin: %d, want 403", rr.Code)
	}
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("root approving: %d %s", rr.Code, rr.Body.String())
	}
	if got := f.permissions(t, "a3"); !slices.Equal(got, []string{permUserSuspend}) {
		t.Fatalf("permissions = %v, want user.suspend", got)
	}
	if profile, err := f.ds.GetUserProfile(context.Background(), f.ids["a3"]); err != nil || profile == nil ||
		!slices.Equal(profile.Permissions, []string{permUserSuspend}) {
		t.Fatalf("profile = %+v (%v), want it re-countersigned with user.suspend", profile, err)
	}
}

func TestDemote_ClearsPermissionsAndRevokesAdminInvites(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionDemote)
	f.grant(t, "a1", permAdminDemote)
	userInvite := f.unclaimedInvite(t, "a3")
	adminInvite := f.unclaimedInvite(t, "a3")
	if _, err := f.db.Exec(`UPDATE invites SET granted_role = 'admin' WHERE id = $1`, adminInvite); err != nil {
		t.Fatal(err)
	}

	msg := f.request(t, "a1", adminActionDemote, "Inactive", "", "a3")
	f.openAs(t, "a1", msg)
	f.grant(t, "a2", permAdminDemote)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Fatalf("admin approving a demotion: %d, want 403", rr.Code)
	}
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("root approving: %d %s", rr.Code, rr.Body.String())
	}
	if role := queryUserRole(t, f.db, f.ids["a3"]); role != roleUser {
		t.Fatalf("role = %s, want user", role)
	}
	if got := f.permissions(t, "a3"); len(got) != 0 {
		t.Fatalf("permissions = %v, want none", got)
	}
	for id, wantRevoked := range map[string]bool{userInvite: false, adminInvite: true} {
		var revoked bool
		if err := f.db.QueryRow(`SELECT revoked_at IS NOT NULL FROM invites WHERE id = $1`, id).Scan(&revoked); err != nil || revoked != wantRevoked {
			t.Errorf("invite %s revoked=%v err=%v, want %v", id, revoked, err, wantRevoked)
		}
	}
}

// claimAdminInvite signs newadmin up with inviteID, logging the claim as
// signup does.
func (f *adminFixture) claimAdminInvite(t *testing.T, inviteID string) error {
	t.Helper()
	inv, err := f.ds.getInviteByID(context.Background(), inviteID)
	if err != nil || inv == nil {
		t.Fatalf("invite %v %v", inv, err)
	}
	in := signupInput("newadmin", "newadmin", inv)
	in.OnAdminClaim = func(tx *sql.Tx) error {
		entry, err := adminInviteClaimEntryTx(context.Background(), tx, inv.ID, string(canonicalID(f.srv, "newadmin")), f.srv)
		if err != nil {
			return err
		}
		return f.h.appendLedgerTx(context.Background(), tx, entry)
	}
	_, err = f.ds.Signup(context.Background(), in)
	return err
}

func TestAdminInvite_PermissionsCheckedLoggedAndGrantedOnClaim(t *testing.T) {
	f := newAdminFixture(t)
	f.h.cfg.MaxInvitesPerUser = maxInvitesUnlimited
	f.grant(t, "a1", permAdminCreate)
	creator := f.ids["a1"]
	kp := f.keys["a1"]

	invite := func(perms string) *httptestResponse {
		body := inviteCreateBody(t, f.h, creator, kp, "", "", time.Now(), roleAdmin)
		var msg pb.CreateInviteRequest
		if err := proto.Unmarshal(body.Bytes(), &msg); err != nil {
			t.Fatal(err)
		}
		msg.Permissions = perms
		b, _ := proto.Marshal(&msg)
		rr := postCreateInvite(f.h, creator, bytes.NewBuffer(b))
		return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
	}
	if rr := invite(permUserRebind); rr.code != http.StatusForbidden {
		t.Fatalf("granting a permission the inviter lacks: %d, want 403", rr.code)
	}
	rr := invite(formatPermissionSet([]string{permUserKick, permAdminCreate}))
	if rr.code != http.StatusCreated {
		t.Fatalf("admin invite: %d %q", rr.code, rr.body)
	}
	var created pb.Invite
	decodeProto(t, rr.body, &created)
	if entries := f.ledger(t); len(entries) != 0 {
		t.Fatalf("ledger = %+v, want nothing until the invite is claimed", entries)
	}

	if err := f.claimAdminInvite(t, created.Id); err != nil {
		t.Fatal(err)
	}
	newAdmin := string(canonicalID(f.srv, "newadmin"))
	perms, err := f.ds.GetUserPermissions(context.Background(), newAdmin)
	if err != nil || !slices.Equal(perms, []string{permAdminCreate, permUserKick}) {
		t.Fatalf("claimed permissions = %v err=%v", perms, err)
	}
	entries := f.ledger(t)
	if len(entries) != 1 {
		t.Fatalf("ledger = %+v, want the claim entry", entries)
	}
	e := entries[0]
	if e.Kind != ledgerKindAdminInvite || e.UserID != newAdmin || e.RequesterID != creator ||
		e.Message != "["+created.Id+"] Admin invite claimed" || e.Content != "admin.create,user.kick" || len(e.Targets) != 0 {
		t.Errorf("claim entry = %+v", e)
	}
	if !e.RequestedAt.Equal(timeFromUnix(created.ServerSignature.GetSignedAt())) || e.RequestedAt.After(e.ServerSignature.SignedAt) {
		t.Errorf("requestedAt = %v, want the invite's countersignature time", e.RequestedAt)
	}
	f.verifyChain(t, entries)
}

func TestAdminInvite_ClaimRefusedOnceInviterLostAGrantedPermission(t *testing.T) {
	f := newAdminFixture(t)
	f.h.cfg.MaxInvitesPerUser = maxInvitesUnlimited
	f.grant(t, "a1", permAdminCreate)
	creator, kp := f.ids["a1"], f.keys["a1"]
	body := inviteCreateBody(t, f.h, creator, kp, "", "", time.Now(), roleAdmin)
	var msg pb.CreateInviteRequest
	if err := proto.Unmarshal(body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	msg.Permissions = permUserKick
	b, _ := proto.Marshal(&msg)
	rr := postCreateInvite(f.h, creator, bytes.NewBuffer(b))
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin invite: %d %q", rr.Code, rr.Body.String())
	}
	var created pb.Invite
	decodeProto(t, rr.Body.Bytes(), &created)

	if _, err := f.db.Exec(`DELETE FROM user_permissions WHERE user_id = $1 AND permission = $2`, creator, permUserKick); err != nil {
		t.Fatal(err)
	}
	if err := f.claimAdminInvite(t, created.Id); !errors.Is(err, errInviterLacksGrant) {
		t.Fatalf("claim = %v, want errInviterLacksGrant", err)
	}
	if entries := f.ledger(t); len(entries) != 0 {
		t.Fatalf("ledger = %+v, want none", entries)
	}
}

type httptestResponse struct {
	code int
	body []byte
}

func TestGetUserPermissions_SelfOrAdmin(t *testing.T) {
	f := newAdminFixture(t)
	read := func(caller, target string) (int, *pb.UserPermissions) {
		rr := f.serve(t, f.h.GetUserPermissions, http.MethodGet, "/api/admin/users/"+f.ids[target]+"/permissions", caller, nil,
			map[string]string{"userID": f.ids[target]})
		var out pb.UserPermissions
		_ = proto.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, &out
	}
	if code, out := read("u1", "u1"); code != http.StatusOK || out.Role != roleUser || len(out.Permissions) != 0 {
		t.Errorf("own permissions: %d %+v", code, out)
	}
	if code, _ := read("u1", "a1"); code != http.StatusForbidden {
		t.Errorf("a member reading an admin's: %d, want 403", code)
	}
	if code, out := read("a2", "a1"); code != http.StatusOK || !slices.Equal(out.Permissions, []string{permUserKick, permUserSuspend}) {
		t.Errorf("an admin reading another's: %d %+v", code, out)
	}
	if code, out := read("root", "root"); code != http.StatusOK || len(out.Permissions) != len(adminPermissions) {
		t.Errorf("root's own: %d %+v, want every permission", code, out)
	}
}
