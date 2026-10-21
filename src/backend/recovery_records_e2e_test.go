//go:build !ops

package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "syrinx/proto"
)

// reedLinks is what the SPA's copy of reedID says it answers, echoes,
// continues and mentions.
func (s *e2eServer) reedLinks(t *testing.T, reedID string) *pb.RecoveryReedRequest {
	t.Helper()
	links := &pb.RecoveryReedRequest{}
	var head *string
	var index *int
	if err := s.db.QueryRow(`SELECT thread_head, thread_index FROM reeds WHERE id = $1`, reedID).Scan(&head, &index); err != nil {
		t.Fatal(err)
	}
	if head != nil {
		links.ThreadHead, links.ThreadIndex = *head, uint32(*index)
	}
	_ = s.db.QueryRow(`SELECT parent_reed_id, root_id FROM reed_replies WHERE reed_id = $1`, reedID).Scan(&links.ReplyingTo, &links.ReplyingRoot)
	_ = s.db.QueryRow(`SELECT echoed_reed_id FROM reed_echoes WHERE echoing_reed_id = $1`, reedID).Scan(&links.Echoing)
	links.Mentions = queryRows(t, s.db, `SELECT mentioned_user_id FROM reed_mentions WHERE mentioning_reed_id = '`+reedID+`'`)
	for _, server := range queryRows(t, s.db, `SELECT server_id FROM reed_admin_mentions WHERE mentioning_reed_id = '`+reedID+`'`) {
		links.Mentions = append(links.Mentions, adminsHandle+"@"+server)
	}
	return links
}

// mintRoot creates root the way root.go does on first boot, with a device.
func (s *e2eServer) mintRoot(t *testing.T) *e2eUser {
	t.Helper()
	ds, c, key := s.h.services.db, s.h.services.crypto, s.h.signingKey
	serverID := ds.GetServerID()
	kp, err := c.createKeyPair(rootUserID+"@"+serverID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	rootID := string(canonicalID(serverID, rootUserID))
	keyID := string(appendEntity(identityID(rootID), kp.Fingerprint))
	userSig, err := c.sign(context.Background(), string(buildUserIdentityPayload(rootUsername, keyID, "")), kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	profileSig, err := rootCountersign(c, ds, &key, buildNewProfilePayload(rootID, rootUsername, keyID, serverID,
		key.Fingerprint, userSig, "", roleRoot, nil, now), now)
	if err != nil {
		t.Fatal(err)
	}
	keySig, err := rootCountersign(c, ds, &key, buildPublicKeyPayload(serverID, rootID, keyID, key.Fingerprint, kp.PublicKey, now), now)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := uuid.NewString()
	if _, err := ds.Signup(context.Background(), SignupInput{
		UserID: rootUserID, Username: rootUsername, PublicKeyArmor: kp.PublicKey, Fingerprint: keyID,
		KeyCreatedAt: now, UserSignature: userSig, MemberSince: now, ProfileSignature: profileSig,
		PublicKeySignature: keySig, DeviceID: deviceID,
	}); err != nil {
		t.Fatal(err)
	}
	return &e2eUser{id: rootID, keyID: keyID, kp: *kp, deviceID: deviceID}
}

func (s *e2eServer) sign(t *testing.T, u *e2eUser, payload []byte) string {
	t.Helper()
	sig, err := s.h.services.crypto.sign(context.Background(), string(payload), u.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func (u *e2eUser) fingerprint() string {
	_, _, fp, _ := parseKeyFingerprint(identityID(u.keyID))
	return fp
}

// adminRequest opens a request signed by requester and returns its id.
func (s *e2eServer) adminRequest(t *testing.T, requester *e2eUser, action, reason, body string, targets ...*e2eUser) string {
	t.Helper()
	ids := make([]string, len(targets))
	for i, target := range targets {
		ids[i] = target.id
	}
	slices.Sort(ids)
	id := uuid.NewString()
	createdAt := time.Now().UTC().Truncate(time.Second)
	sig := s.sign(t, requester, buildAdminRequestPayload(id, action, requester.id, requester.keyID, reason, ids, body, createdAt))
	msg := &pb.AdminRequest{Id: id, Action: action, RequesterId: requester.id, Reason: reason, Targets: ids, Body: body,
		CreatedAt: unixOrZero(createdAt), RequesterSignature: &pb.UserSignature{Id: requester.keyID, Armor: sig}}
	expectStatus(t, "open "+action, s.authed(t, requester, http.MethodPost, "/api/admin/requests", msg, nil, s.h.OpenAdminRequest), http.StatusCreated)
	return id
}

func (s *e2eServer) decide(t *testing.T, decider *e2eUser, id, decision string) {
	t.Helper()
	req, err := s.h.services.db.GetAdminRequest(context.Background(), id)
	if err != nil || req == nil {
		t.Fatalf("load request %s: %v", id, err)
	}
	sig := s.sign(t, decider, buildAdminDecisionPayload(id, decision, decider.id, decider.keyID, req.RequesterSignature.Armor))
	msg := &pb.AdminDecision{Decision: decision, DeciderId: decider.id, Signature: &pb.UserSignature{Id: decider.keyID, Armor: sig}}
	rr := s.authed(t, decider, http.MethodPost, "/api/admin/requests/"+id+"/decision", msg, map[string]string{"id": id}, s.h.DecideAdminRequest)
	expectStatus(t, decision+" "+req.Action, rr, http.StatusOK)
}

func (s *e2eServer) like(t *testing.T, u *e2eUser, reedID string) {
	t.Helper()
	author, _ := authorOf(identityID(reedID))
	_, _, bare, _ := parseKeyFingerprint(identityID(reedID))
	msg := &pb.LikeRequest{Signature: s.sign(t, u, buildReedLikeUserPayload(reedID, u.keyID)), Fingerprint: u.fingerprint()}
	vars := map[string]string{"userID": string(author), "reedID": bare}
	expectStatus(t, "like", s.authed(t, u, http.MethodPost, "/api/reeds/"+string(author)+"/"+bare+"/like", msg, vars, s.h.LikeReed), http.StatusOK)
}

func (s *e2eServer) block(t *testing.T, u, target *e2eUser) {
	t.Helper()
	msg := &pb.BlockRequest{Signature: s.sign(t, u, buildBlockUserPayload(u.id, target.id, u.keyID)), Fingerprint: u.fingerprint()}
	vars := map[string]string{"userID": target.id}
	expectStatus(t, "block", s.authed(t, u, http.MethodPost, "/api/users/"+target.id+"/block", msg, vars, s.h.BlockUser), http.StatusOK)
}

// vouch has u vouch for subject's key while subject shows their code.
func (s *e2eServer) vouch(t *testing.T, u, subject *e2eUser) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO online_users (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, subject.id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO verification_windows (id, user_id, last_ping_at) VALUES ($1, $2, NOW() AT TIME ZONE 'UTC')
		ON CONFLICT (user_id) DO UPDATE SET last_ping_at = EXCLUDED.last_ping_at
	`, uuid.NewString(), subject.id); err != nil {
		t.Fatal(err)
	}
	msg := &pb.CreateVouchRequest{SubjectKeyId: subject.keyID, VoucherKeyId: u.keyID, Note: "met in person",
		Signature: s.sign(t, u, buildVouchUserPayload(u.keyID, subject.keyID, "met in person"))}
	expectStatus(t, "vouch", s.authed(t, u, http.MethodPost, "/api/vouches", msg, nil, s.h.CreateVouch), http.StatusOK)
}

func (s *e2eServer) withdrawVouch(t *testing.T, u, subject *e2eUser) {
	t.Helper()
	cert, err := s.h.services.db.GetVouch(context.Background(), u.id, subject.keyID)
	if err != nil || cert == nil {
		t.Fatalf("load vouch: %v", err)
	}
	msg := &pb.WithdrawVouchRequest{VoucherKeyId: u.keyID, Signature: s.sign(t, u, buildVouchWithdrawalUserPayload(cert.ID))}
	vars := map[string]string{"subjectKeyID": subject.keyID}
	expectStatus(t, "withdraw vouch", s.authed(t, u, http.MethodDelete, "/api/vouches/"+subject.keyID, msg, vars, s.h.WithdrawVouch), http.StatusOK)
}

func (s *e2eServer) removeReed(t *testing.T, u *e2eUser, reedID string) {
	t.Helper()
	_, _, bare, _ := parseKeyFingerprint(identityID(reedID))
	msg := &pb.RemovalRequest{Signature: s.sign(t, u, buildReedRemovalUserPayload(s.h.services.db.GetServerID(), reedID))}
	vars := map[string]string{"userID": u.id, "reedID": bare}
	expectStatus(t, "remove reed", s.authed(t, u, http.MethodDelete, "/api/reeds/"+u.id+"/"+bare, msg, vars, s.h.DeleteReed), http.StatusOK)
}

func (s *e2eServer) deleteAccount(t *testing.T, u *e2eUser) {
	t.Helper()
	msg := &pb.DeleteAccountRequest{Signature: s.sign(t, u, buildAccountRemovalUserPayload(s.h.services.db.GetServerID(), u.id, "bye"))}
	msg.Note = "bye"
	expectStatus(t, "delete account", s.authed(t, u, http.MethodDelete, "/api/users/me", msg, nil, s.h.DeleteMe), http.StatusOK)
}

// thread posts an n-part thread by u and returns its id.
func (s *e2eServer) thread(t *testing.T, u *e2eUser, n int) string {
	t.Helper()
	serverID := s.h.services.db.GetServerID()
	msg := &pb.CreateThreadRequest{}
	_ = s.db.QueryRow(`SELECT id FROM reeds WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, u.id).Scan(&msg.PreviousId)
	var ids []string
	for range n {
		id := string(appendEntity(identityID(u.id), uuid.Must(uuid.NewV7()).String()))
		ids = append(ids, id)
		msg.Reeds = append(msg.Reeds, &pb.ThreadPartRequest{ReedId: id, Signature: s.sign(t, u, []byte("content of "+id))})
	}
	msg.ThreadSignature = s.sign(t, u, buildThreadUserPayload(serverID, ids[0], ids))
	expectStatus(t, "thread", s.authed(t, u, http.MethodPost, "/api/threads", msg, nil, s.h.CreateThread), http.StatusOK)
	return ids[0]
}

func (s *e2eServer) removeThread(t *testing.T, u *e2eUser, threadID string) {
	t.Helper()
	rec, err := s.h.services.db.GetThreadRecord(context.Background(), threadID)
	if err != nil || rec == nil {
		t.Fatalf("load thread %s: %v", threadID, err)
	}
	msg := &pb.RemovalRequest{Signature: s.sign(t, u, buildThreadRemovalUserPayload(s.h.services.db.GetServerID(), threadID, rec.UserSignature))}
	vars := map[string]string{"threadID": threadID}
	expectStatus(t, "remove thread", s.authed(t, u, http.MethodDelete, "/api/threads/"+threadID, msg, vars, s.h.DeleteThread), http.StatusOK)
}

// flag flags reedID as u and returns the signed flag u's device keeps.
func (s *e2eServer) flag(t *testing.T, u *e2eUser, reedID string) *pb.ReedFlag {
	t.Helper()
	createdAt := time.Now().UTC().Truncate(time.Second)
	msg := &pb.ReedFlag{ReedId: reedID, ReporterId: u.id, Note: "spam", CreatedAt: unixOrZero(createdAt),
		Signature: &pb.UserSignature{Id: u.keyID, Armor: s.sign(t, u, buildReedFlagPayload(reedID, u.id, u.keyID, "spam", createdAt))}}
	expectStatus(t, "flag", s.authed(t, u, http.MethodPost, "/api/flags", msg, nil, s.h.FlagReed), http.StatusNoContent)
	return msg
}

func (s *e2eServer) pin(t *testing.T, u *e2eUser, reedID string) {
	t.Helper()
	vars := map[string]string{"reedID": reedID}
	expectStatus(t, "pin", s.authed(t, u, http.MethodPost, "/api/reeds/"+reedID+"/pin", &pb.PinRequest{}, vars, s.h.PinReed), http.StatusNoContent)
}

// heldRecords is every signed record u's device holds, as the API served
// them: its own likes, blocks and flags, and everything public it read.
func (s *e2eServer) heldRecords(t *testing.T, u *e2eUser, flags []*pb.ReedFlag) []*pb.RecoveryRecord {
	t.Helper()
	ctx := context.Background()
	ds := s.h.services.db
	var out []*pb.RecoveryRecord
	add := func(r *pb.RecoveryRecord) { out = append(out, r) }

	for _, e := range queryRows(t, s.db, `SELECT id FROM ledger_entries ORDER BY id`) {
		var id uint64
		if _, err := fmt.Sscan(e, &id); err != nil {
			t.Fatal(err)
		}
		entry, err := ds.GetLedgerEntry(ctx, id)
		if err != nil || entry == nil {
			t.Fatalf("ledger %d: %v", id, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_LedgerEntry{LedgerEntry: pbLedgerEntry(entry)}})
	}
	for _, id := range queryRows(t, s.db, `SELECT id FROM admin_requests WHERE state <> 'pending'`) {
		req, err := ds.GetAdminRequest(ctx, id)
		if err != nil || req == nil {
			t.Fatalf("admin request %s: %v", id, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_AdminRequest{AdminRequest: pbAdminRequest(req)}})
	}
	for _, id := range queryRows(t, s.db, `SELECT reed_id FROM reed_removals WHERE thread_id IS NULL`) {
		w, err := ds.GetReedRemovalWire(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_ReedRemoval{ReedRemoval: pbReedRemovalCert(w)}})
	}
	for _, id := range queryRows(t, s.db, `SELECT user_id FROM account_removals`) {
		w, err := ds.GetAccountRemovalWire(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_AccountRemoval{AccountRemoval: pbAccountRemovalCert(w)}})
	}
	for _, id := range queryRows(t, s.db, `SELECT id FROM reed_threads WHERE id NOT IN (SELECT thread_id FROM thread_removals)`) {
		rec, err := ds.GetThreadRecord(ctx, id)
		if err != nil || rec == nil {
			t.Fatalf("thread %s: %v", id, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_Thread{Thread: pbThreadRecord(rec.wire(ds.GetServerID()))}})
	}
	for _, id := range queryRows(t, s.db, `SELECT thread_id FROM thread_removals`) {
		rm, err := ds.GetThreadRemoval(ctx, id)
		if err != nil || rm == nil {
			t.Fatalf("thread removal %s: %v", id, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_ThreadRemoval{ThreadRemoval: pbThreadRemoval(rm)}})
	}
	for _, id := range queryRows(t, s.db, `SELECT id FROM user_vouches`) {
		cert, err := ds.GetVouchByID(ctx, id)
		if err != nil || cert == nil {
			t.Fatalf("vouch %s: %v", id, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_Vouch{Vouch: pbVouch(cert)}})
	}
	for _, reedID := range queryRows(t, s.db, `SELECT reed_id FROM reeds_liked WHERE liker_user_id = '`+u.id+`'`) {
		cert, err := ds.GetReedLike(ctx, u.id, reedID)
		if err != nil || cert == nil {
			t.Fatalf("like %s: %v", reedID, err)
		}
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_Like{Like: pbLikeCert(cert)}})
	}
	blocks, err := ds.ListBlocksByUser(ctx, u.id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range blocks {
		add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_Block{Block: pbBlockCert(&blocks[i])}})
	}
	for _, f := range flags {
		if f.GetReporterId() == u.id {
			add(&pb.RecoveryRecord{Record: &pb.RecoveryRecord_Flag{Flag: f}})
		}
	}
	return out
}
