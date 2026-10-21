//go:build !ops

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// adminFixture is root, three admins and two users, all local with real
// keypairs. a3 was invited by a1.
type adminFixture struct {
	h        *Handlers
	ds       *DataService
	db       *sql.DB
	srv      string
	ids      map[string]string
	keys     map[string]cryptoKeyPair
	executed []string
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	broadcasts := make(chan realtimeBroadcastMessage, 64)
	h.broadcastChan = broadcasts
	go func() {
		for range broadcasts {
		}
	}()
	t.Cleanup(func() { close(broadcasts) })
	f := &adminFixture{h: h, ds: h.services.db, db: db, srv: h.services.db.GetServerID(),
		ids: map[string]string{}, keys: map[string]cryptoKeyPair{}}
	for name, bare := range map[string]string{"root": rootUserID, "a1": "a1", "a2": "a2", "a3": "a3", "u1": "u1", "u2": "u2"} {
		f.keys[name] = signedUpUser(t, h, bare, name)
		f.ids[name] = string(canonicalID(f.srv, bare))
	}
	f.setRole(t, "root", roleRoot)
	for _, a := range []string{"a1", "a2", "a3"} {
		f.setRole(t, a, roleAdmin)
		f.grant(t, a, permUserKick, permUserSuspend)
	}
	f.setInvitedBy(t, "a1", "a3")

	stub := adminExecutor{execute: func(_ context.Context, _ *sql.Tx, req *adminRequest) error {
		f.executed = append(f.executed, req.ID)
		return nil
	}}
	h.adminExecutorOverride = map[string]adminExecutor{
		adminActionKick: stub, adminActionSuspend: stub, adminActionBroadcast: stub,
		adminActionUnicast: stub, adminActionPromote: stub, adminActionPermissions: stub,
	}
	return f
}

func (f *adminFixture) setRole(t *testing.T, name, role string) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE users SET role = $1 WHERE id = $2`, role, f.ids[name]); err != nil {
		t.Fatal(err)
	}
}

func (f *adminFixture) grant(t *testing.T, name string, perms ...string) {
	t.Helper()
	for _, p := range perms {
		if _, err := f.db.Exec(`INSERT INTO user_permissions (user_id, permission) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, f.ids[name], p); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *adminFixture) revoke(t *testing.T, name string, perm string) {
	t.Helper()
	if _, err := f.db.Exec(`DELETE FROM user_permissions WHERE user_id = $1 AND permission = $2`, f.ids[name], perm); err != nil {
		t.Fatal(err)
	}
}

// setInvitedBy records that inviter invited invitee.
func (f *adminFixture) setInvitedBy(t *testing.T, inviter, invitee string) {
	t.Helper()
	var sigID int64
	if err := f.db.QueryRow(`INSERT INTO user_signatures (public_key_id, signature) VALUES ('k', 's') RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	var serverSigID int64
	if err := f.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id`).Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	inviteID := f.ids[inviter] + "/" + uuid.NewString()
	if _, err := f.db.Exec(`INSERT INTO invites (id, created_by, token_hash, claimed_by, granted_role, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, 'admin', $5, $6)`, inviteID, f.ids[inviter], []byte(inviteID), f.ids[invitee], sigID, serverSigID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET invite_id = $1 WHERE id = $2`, inviteID, f.ids[invitee]); err != nil {
		t.Fatal(err)
	}
}

func (f *adminFixture) keyID(name string) string {
	return string(appendEntity(identityID(f.ids[name]), f.keys[name].Fingerprint))
}

// request builds a signed request from requester.
func (f *adminFixture) request(t *testing.T, requester, action, reason, body string, targets ...string) *pb.AdminRequest {
	t.Helper()
	ids := make([]string, len(targets))
	for i, name := range targets {
		ids[i] = f.ids[name]
	}
	sort.Strings(ids)
	createdAt := time.Now().UTC().Truncate(time.Second)
	id := uuid.NewString()
	payload := buildAdminRequestPayload(id, action, f.ids[requester], f.keyID(requester), reason, ids, body, createdAt)
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[requester].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.AdminRequest{
		Id: id, Action: action, RequesterId: f.ids[requester], Reason: reason, Targets: ids, Body: body,
		CreatedAt:          unixOrZero(createdAt),
		RequesterSignature: &pb.UserSignature{Id: f.keyID(requester), Armor: sig},
	}
}

func (f *adminFixture) serve(t *testing.T, handler http.HandlerFunc, method, path, caller string, msg proto.Message, vars map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := signedRequest(t, f.h, method, path, f.ids[caller], f.keys[caller].Fingerprint, f.keys[caller].PrivateKey, msg)
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	f.h.signatureAuthMiddleware("/api")(handler).ServeHTTP(rr, req)
	return rr
}

// openAs opens msg, signed in as requester, and fails unless it's stored.
func (f *adminFixture) openAs(t *testing.T, requester string, msg *pb.AdminRequest) {
	t.Helper()
	rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", requester, msg, nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("open status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// decide has decider sign and submit decision on the stored request id.
func (f *adminFixture) decide(t *testing.T, decider, id, decision string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := f.ds.GetAdminRequest(context.Background(), id)
	if err != nil || req == nil {
		t.Fatalf("load request: %v %v", req, err)
	}
	payload := buildAdminDecisionPayload(id, decision, f.ids[decider], f.keyID(decider), req.RequesterSignature.Armor)
	sig, err := f.h.services.crypto.sign(context.Background(), string(payload), f.keys[decider].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	msg := &pb.AdminDecision{Decision: decision, DeciderId: f.ids[decider],
		Signature: &pb.UserSignature{Id: f.keyID(decider), Armor: sig}}
	return f.serve(t, f.h.DecideAdminRequest, http.MethodPost, "/api/admin/requests/"+id+"/decision",
		decider, msg, map[string]string{"id": id})
}

func (f *adminFixture) state(t *testing.T, id string) string {
	t.Helper()
	req, err := f.ds.GetAdminRequest(context.Background(), id)
	if err != nil || req == nil {
		t.Fatalf("load request: %v %v", req, err)
	}
	return req.State
}

func TestOpenAdminRequest_Validation(t *testing.T) {
	f := newAdminFixture(t)
	unsorted := f.request(t, "a1", adminActionKick, "Spam", "", "u1", "u2")
	unsorted.Targets = []string{unsorted.Targets[1], unsorted.Targets[0]}

	cases := []struct {
		name string
		msg  *pb.AdminRequest
		want int
	}{
		{"root target", f.request(t, "a1", adminActionKick, "Spam", "", "root"), http.StatusForbidden},
		{"no reason", f.request(t, "a1", adminActionKick, "", "", "u1"), http.StatusBadRequest},
		{"broadcast with a reason", f.request(t, "root", adminActionBroadcast, "Why", "Hi"), http.StatusBadRequest},
		{"broadcast too long", f.request(t, "root", adminActionBroadcast, "", string(make([]byte, 141))), http.StatusBadRequest},
		{"message with two targets", f.request(t, "root", adminActionUnicast, "", "Hi", "u1", "u2"), http.StatusBadRequest},
		{"unknown permission", f.request(t, "root", adminActionPromote, "Helps", "user.fly", "u1"), http.StatusBadRequest},
		{"unsorted permissions", f.request(t, "root", adminActionPromote, "Helps", "user.kick,admin.create", "u1"), http.StatusBadRequest},
		{"missing permission", f.request(t, "a1", adminActionBroadcast, "", "Hi"), http.StatusForbidden},
		{"granting a permission not held", f.request(t, "a1", adminActionPromote, "Helps", "user.kick", "u1"), http.StatusForbidden},
		{"unknown action", f.request(t, "a1", "ban", "Spam", "", "u1"), http.StatusBadRequest},
		{"bad signature", func() *pb.AdminRequest {
			m := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
			m.Reason = "Changed"
			return m
		}(), http.StatusUnauthorized},
	}
	for _, c := range cases {
		requester := "a1"
		if c.msg.RequesterId == f.ids["root"] {
			requester = "root"
		}
		rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", requester, c.msg, nil)
		if rr.Code != c.want {
			t.Errorf("%s: status=%d body=%s, want %d", c.name, rr.Code, rr.Body.String(), c.want)
		}
	}

	rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", unsorted, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unsorted targets: status=%d, want 400", rr.Code)
	}

	nonAdmin := f.request(t, "u1", adminActionKick, "Spam", "", "u2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "u1", nonAdmin, nil); rr.Code != http.StatusForbidden {
		t.Errorf("non-admin: status=%d, want 403", rr.Code)
	}

	ok := f.request(t, "a1", adminActionKick, "Spam", "", "u1", "u2")
	f.openAs(t, "a1", ok)
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", ok, nil); rr.Code != http.StatusConflict {
		t.Errorf("duplicate ID: status=%d, want 409", rr.Code)
	}
}

func TestAdminDecision_ApproverEligibility(t *testing.T) {
	f := newAdminFixture(t)
	// An invitee's invitee may still decide.
	f.setInvitedBy(t, "a3", "a2")
	msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", msg)

	refused := []struct {
		decider, why string
	}{
		{"a1", "requester"},
		{"a3", "invited by the requester"},
		{"u2", "not an admin"},
	}
	for _, c := range refused {
		if rr := f.decide(t, c.decider, msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
			t.Errorf("%s (%s): status=%d body=%s, want 403", c.decider, c.why, rr.Code, rr.Body.String())
		}
	}

	f.revoke(t, "a2", permUserKick)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Errorf("approver without the permission: status=%d, want 403", rr.Code)
	}
	f.grant(t, "a2", permUserKick)

	if got := f.state(t, msg.Id); got != adminStatePending {
		t.Fatalf("state after refusals = %s, want pending", got)
	}
	rr := f.decide(t, "a2", msg.Id, adminDecisionApprove)
	if rr.Code != http.StatusOK {
		t.Fatalf("eligible approve: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !slices.Contains(f.executed, msg.Id) || f.state(t, msg.Id) != adminStateExecuted {
		t.Fatalf("executed=%v state=%s, want executed", f.executed, f.state(t, msg.Id))
	}
	req, _ := f.ds.GetAdminRequest(context.Background(), msg.Id)
	if req.Decision == nil || req.Decision.DeciderID != f.ids["a2"] || req.ServerSignature == nil {
		t.Fatalf("closed request = %+v, want a2's decision and a server signature", req)
	}
	if rr := f.decide(t, "root", msg.Id, adminDecisionReject); rr.Code != http.StatusConflict {
		t.Errorf("decision on a closed request: status=%d, want 409", rr.Code)
	}
}

func TestAdminDecision_InviteeDecidesOnlyNonDestructive(t *testing.T) {
	f := newAdminFixture(t)
	f.grant(t, "a1", permAdminCreate)
	f.grant(t, "a3", permAdminCreate)
	promote := f.request(t, "a1", adminActionPromote, "Trusted", permUserSuspend, "u2")
	f.openAs(t, "a1", promote)
	rr := f.decide(t, "a3", promote.Id, adminDecisionApprove)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "invited") {
		t.Errorf("invitee on a promotion: status=%d body=%s, want 403 for the invite", rr.Code, rr.Body.String())
	}

	suspend := f.request(t, "a1", adminActionSuspend, "Rude", "", "u1")
	f.openAs(t, "a1", suspend)
	if rr := f.decide(t, "a3", suspend.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("invitee on a suspension: status=%d body=%s, want 200", rr.Code, rr.Body.String())
	}
}

func TestAdminDecision_TargetAndAdminTargetRules(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "a1", adminActionSuspend, "Rude", "", "a2")
	f.openAs(t, "a1", msg)

	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Errorf("target deciding: status=%d, want 403", rr.Code)
	}
	f.setRole(t, "u2", roleAdmin)
	f.grant(t, "u2", permUserSuspend)
	if rr := f.decide(t, "u2", msg.Id, adminDecisionApprove); rr.Code != http.StatusForbidden {
		t.Errorf("admin approving a request about an admin: status=%d, want 403", rr.Code)
	}
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Errorf("root approving a request about an admin: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminDecision_RootApprovesOwnRequest(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "root", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "root", msg)
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("root self-approve: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminDecision_RejectAndWithdraw(t *testing.T) {
	f := newAdminFixture(t)
	rejected := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", rejected)
	if rr := f.decide(t, "a2", rejected.Id, adminDecisionReject); rr.Code != http.StatusOK {
		t.Fatalf("reject: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, rejected.Id); got != adminStateRejected || len(f.executed) != 0 {
		t.Fatalf("state=%s executed=%v, want rejected and nothing executed", got, f.executed)
	}

	withdrawn := f.request(t, "a1", adminActionKick, "Spam", "", "u2")
	f.openAs(t, "a1", withdrawn)
	if rr := f.decide(t, "a2", withdrawn.Id, adminDecisionWithdraw); rr.Code != http.StatusForbidden {
		t.Errorf("withdraw by someone else: status=%d, want 403", rr.Code)
	}
	if rr := f.decide(t, "a1", withdrawn.Id, adminDecisionWithdraw); rr.Code != http.StatusOK {
		t.Fatalf("withdraw: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := f.state(t, withdrawn.Id); got != adminStateWithdrawn {
		t.Fatalf("state=%s, want withdrawn", got)
	}
}

func TestAdminDecision_RequestOutlivesRequesterPermission(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", msg)
	f.revoke(t, "a1", permUserKick)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve after requester lost the permission: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminRequest_PermissionsChangeNeedsAddAndRemovePermissions(t *testing.T) {
	f := newAdminFixture(t)
	f.grant(t, "a1", permAdminCreate)

	adds := f.request(t, "a1", adminActionPermissions, "Trusted", formatPermissionSet([]string{permUserKick, permUserSuspend, permAdminCreate}), "a2")
	f.openAs(t, "a1", adds)

	removes := f.request(t, "a1", adminActionPermissions, "Too much", permUserKick, "a2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", removes, nil); rr.Code != http.StatusForbidden {
		t.Errorf("removing without admin.demote: status=%d, want 403", rr.Code)
	}
	f.grant(t, "a1", permAdminDemote)
	f.openAs(t, "a1", f.request(t, "a1", adminActionPermissions, "Too much", permUserKick, "a2"))

	same := f.request(t, "a1", adminActionPermissions, "Same", formatPermissionSet([]string{permUserKick, permUserSuspend}), "a2")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", same, nil); rr.Code != http.StatusConflict {
		t.Errorf("unchanged permissions: status=%d, want 409", rr.Code)
	}
}

func TestAdminRequest_RemovedTargetsDropAndGoMoot(t *testing.T) {
	f := newAdminFixture(t)
	ctx := context.Background()
	single := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", single)
	pair := f.request(t, "a1", adminActionSuspend, "Rude", "", "u1", "u2")
	f.openAs(t, "a1", pair)

	if _, err := f.db.Exec(`
		WITH us AS (INSERT INTO user_signatures (public_key_id, signature) VALUES ($2, 's') RETURNING id),
		     ss AS (INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id)
		INSERT INTO account_removals (user_id, public_key_id, user_signature_id, server_signature_id)
		SELECT $1, $2, us.id, ss.id FROM us, ss
	`, f.ids["u1"], f.keyID("u1")); err != nil {
		t.Fatal(err)
	}
	f.h.dropFromAdminRequests(ctx, f.ids["u1"])

	req, _ := f.ds.GetAdminRequest(ctx, single.Id)
	if req.State != adminStateMoot || req.ServerSignature == nil || req.Decision != nil {
		t.Fatalf("single-target request = %+v, want moot with a server signature and no decision", req)
	}
	req, _ = f.ds.GetAdminRequest(ctx, pair.Id)
	if req.State != adminStatePending || !slices.Equal(req.Dropped, []string{f.ids["u1"]}) {
		t.Fatalf("two-target request state=%s dropped=%v, want pending with u1 dropped", req.State, req.Dropped)
	}
	if !slices.Equal(req.remainingTargets(), []string{f.ids["u2"]}) {
		t.Fatalf("remaining targets = %v, want u2", req.remainingTargets())
	}
}

func TestListAdminRequests_PagesNewestFirst(t *testing.T) {
	f := newAdminFixture(t)
	ctx := context.Background()
	var opened []string
	for range 3 {
		msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
		f.openAs(t, "a1", msg)
		opened = append(opened, msg.Id)
		if _, err := f.db.Exec(`UPDATE admin_requests SET created_at = created_at - make_interval(secs => $2) WHERE id = $1`,
			msg.Id, 10*len(opened)); err != nil {
			t.Fatal(err)
		}
	}
	if rr := f.decide(t, "a2", opened[2], adminDecisionReject); rr.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", rr.Code, rr.Body.String())
	}

	page, next, err := f.ds.ListAdminRequests(ctx, adminStatePending, "", 1, "", nil)
	if err != nil || len(page) != 1 || page[0].ID != opened[0] || next == "" {
		t.Fatalf("first page = %v next=%q err=%v, want %s", page, next, err, opened[0])
	}
	page, next, err = f.ds.ListAdminRequests(ctx, adminStatePending, next, 1, "", nil)
	if err != nil || len(page) != 1 || page[0].ID != opened[1] || next != "" {
		t.Fatalf("second page = %v next=%q err=%v, want %s and no more", page, next, err, opened[1])
	}
	closed, _, err := f.ds.ListAdminRequests(ctx, "closed", "", 10, "", nil)
	if err != nil || len(closed) != 1 || closed[0].ID != opened[2] {
		t.Fatalf("closed = %v err=%v, want %s", closed, err, opened[2])
	}
}

func TestParsePermissionSet(t *testing.T) {
	if got, err := parsePermissionSet("admin.create,user.kick"); err != nil || !slices.Equal(got, []string{"admin.create", "user.kick"}) {
		t.Errorf("valid set = %v %v", got, err)
	}
	for _, bad := range []string{"user.kick,admin.create", "user.kick,user.kick", "user.kick,", "all"} {
		if _, err := parsePermissionSet(bad); err == nil {
			t.Errorf("%q parsed, want refused", bad)
		}
	}
	if got := formatPermissionSet([]string{permUserKick, permAdminCreate, permUserKick}); got != "admin.create,user.kick" {
		t.Errorf("format = %q", got)
	}
}
