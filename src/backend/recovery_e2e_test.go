//go:build !ops

package main

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

const e2ePassphrase = "recovery-e2e-passphrase"

// e2eServer is a full server on the real schema, signing with a real key.
type e2eServer struct {
	h  *Handlers
	db *sql.DB
}

func newE2EServer(t *testing.T, db *sql.DB, recoveryMode bool) *e2eServer {
	t.Helper()
	ctx := context.Background()
	ds := NewDataService(db, "test")
	if err := ds.InitServer(ctx, recoveryMode, "https://test.example", "https://test.example"); err != nil {
		t.Fatalf("InitServer: %v", err)
	}
	cryptoSvc := newCryptoService()
	key, err := ds.InitServerKey(ctx, cryptoSvc, e2ePassphrase)
	if err != nil {
		t.Fatalf("InitServerKey: %v", err)
	}
	services := &Services{db: ds, crypto: cryptoSvc, log: NewLoggingService()}
	cfg := AppConfig{ServerName: "test", SignupMode: "open", RecoveryMode: recoveryMode, MaxInvitesPerUser: maxInvitesUnlimited}
	return &e2eServer{
		h:  NewHandlers(services, cfg, make(chan realtimeBroadcastMessage, 256), *key),
		db: db,
	}
}

// e2eUser is what a client device holds for its own account.
type e2eUser struct {
	id       string // canonical
	keyID    string // canonical
	kp       cryptoKeyPair
	deviceID string
}

func (s *e2eServer) signup(t *testing.T, username string) *e2eUser {
	t.Helper()
	return s.signupInvited(t, username, "", "")
}

func (s *e2eServer) signupInvited(t *testing.T, username, inviteID, inviteSecret string) *e2eUser {
	t.Helper()
	bare, err := newCryptoID()
	if err != nil {
		t.Fatal(err)
	}
	return s.signupAs(t, bare, username, inviteID, inviteSecret)
}

// signupAs signs up with a chosen bare id, such as root's.
func (s *e2eServer) signupAs(t *testing.T, bare, username, inviteID, inviteSecret string) *e2eUser {
	t.Helper()
	h := s.h
	userIDSig, err := h.services.crypto.sign(context.Background(), bare, h.signingKey.Armor)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := h.services.crypto.createKeyPair(bare, "", "")
	if err != nil {
		t.Fatal(err)
	}
	keySelfSig, err := h.services.crypto.sign(context.Background(), kp.PublicKey, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	self := canonicalID(h.services.db.GetServerID(), bare)
	keyID := string(appendEntity(self, kp.Fingerprint))
	userSig, err := h.services.crypto.sign(context.Background(), string(buildUserIdentityPayload(username, keyID, "")), kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := uuid.NewString()
	req := protoRequest(http.MethodPost, "/api/signup", &pb.SignupRequest{
		Username:          username,
		PublicKey:         kp.PublicKey,
		Signature:         keySelfSig,
		UserSignature:     userSig,
		UserId:            bare,
		UserIdSignature:   userIDSig,
		UserIdFingerprint: h.signingKey.Fingerprint,
		InviteId:          inviteID,
		InviteSecret:      inviteSecret,
	})
	req.Header.Set("X-Syrinx-Device-Id", deviceID)
	rr := httptest.NewRecorder()
	h.Signup(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("signup %s: %d %s", username, rr.Code, errorMessage(t, rr.Body.Bytes()))
	}
	return &e2eUser{id: string(self), keyID: keyID, kp: *kp, deviceID: deviceID}
}

// recoverySnapshot is what a device's IndexedDB holds about one user, in
// the shapes the SPA stores them.
type recoverySnapshot struct {
	profile *pb.User
	keys    map[string]*pb.PublicKey // by canonical key id
	revs    map[string]*pb.KeyRevocationCert
	active  string
}

func (s *e2eServer) snapshot(t *testing.T, userID string) recoverySnapshot {
	t.Helper()
	ctx := context.Background()
	ds := s.h.services.db
	u, err := ds.GetUserProfile(ctx, userID)
	if err != nil || u == nil {
		t.Fatalf("GetUserProfile %s: %v", userID, err)
	}
	info, err := ds.GetUserInfo(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserInfo %s: %v", userID, err)
	}
	snap := recoverySnapshot{
		profile: pbUser(u),
		keys:    map[string]*pb.PublicKey{},
		revs:    map[string]*pb.KeyRevocationCert{},
		active:  info.ActiveKeyID,
	}
	for id := info.ActiveKeyID; id != ""; {
		k, err := ds.GetPublicKey(ctx, id)
		if err != nil || k == nil {
			t.Fatalf("GetPublicKey %s: %v", id, err)
		}
		snap.keys[id] = pbKey(k)
		if k.Revoked {
			rev, err := ds.GetKeyRevocation(ctx, id)
			if err != nil || rev == nil {
				t.Fatalf("GetKeyRevocation %s: %v", id, err)
			}
			snap.revs[id] = pbKeyRevocation(rev)
		}
		if k.Predecessor == nil {
			break
		}
		id = *k.Predecessor
	}
	return snap
}

// spaKeyNest mirrors recoveryKeyNest.ts buildKeyNest.
func spaKeyNest(t *testing.T, snap recoverySnapshot) *pb.RecoveryKeyNode {
	t.Helper()
	var build func(id, link string) *pb.RecoveryKeyNode
	build = func(id, link string) *pb.RecoveryKeyNode {
		k := snap.keys[id]
		if k == nil {
			t.Fatalf("nest: missing key %s", id)
		}
		node := &pb.RecoveryKeyNode{
			Fingerprint:     k.GetId(),
			UserId:          k.GetUserId(),
			Armor:           k.GetArmor(),
			Revoked:         k.GetRevoked(),
			ServerSignature: k.GetServerSignature(),
			CreatedAt:       k.GetCreatedAt(),
			Signature:       link,
		}
		if rev := snap.revs[id]; rev != nil {
			node.Revocation = &pb.RecoveryRevocation{
				Fingerprint:     rev.GetId(),
				UserId:          rev.GetUserId(),
				Reason:          rev.GetReason(),
				Successor:       rev.Successor,
				UserSignature:   rev.GetUserSignature(),
				ServerSignature: rev.GetServerSignature(),
			}
		}
		if k.Predecessor != nil {
			pred := snap.revs[k.GetPredecessor()]
			if pred == nil || pred.GetSuccessorSignature() == "" {
				t.Fatalf("nest: missing handoff signature on %s", id)
			}
			node.Predecessor = build(k.GetPredecessor(), pred.GetSuccessorSignature())
		}
		return node
	}
	return build(snap.active, "")
}

// spaRecoveryProfile mirrors recoveryProfile.ts recoveryProfileOf.
func spaRecoveryProfile(u *pb.User) *pb.RecoveryProfile {
	return &pb.RecoveryProfile{
		Id:              u.GetId(),
		Username:        u.GetUsername(),
		Role:            u.GetRole(),
		MemberSince:     u.GetMemberSince(),
		Bio:             u.GetBio(),
		UserSignature:   u.GetUserSignature(),
		ServerSignature: u.GetServerSignature(),
		Invite:          u.GetInvite(),
		Permissions:     u.GetPermissions(),
	}
}

func (s *e2eServer) claim(t *testing.T, u *e2eUser, snap recoverySnapshot) {
	t.Helper()
	if rr := s.claimRequest(t, u, snap); rr.Code != http.StatusOK {
		t.Fatalf("claim %s: %d %s", u.id, rr.Code, errorMessage(t, rr.Body.Bytes()))
	}
}

// claimRequest runs the claim and returns the response, whatever it is.
func (s *e2eServer) claimRequest(t *testing.T, u *e2eUser, snap recoverySnapshot) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.h.IssueChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil))
	var ch pb.ChallengeResponse
	decodeProto(t, rr.Body.Bytes(), &ch)
	sig, err := s.h.services.crypto.sign(context.Background(), ch.GetChallenge(), u.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	req := protoRequest(http.MethodPost, "/api/recovery/identity/claim", &pb.ClaimIdentityRequest{
		Challenge: ch.GetChallenge(),
		Signature: sig,
		Profile:   spaRecoveryProfile(snap.profile),
		Key:       spaKeyNest(t, snap),
	})
	req.Header.Set("X-Syrinx-Device-Id", u.deviceID)
	rr = httptest.NewRecorder()
	s.h.ClaimIdentity(rr, req)
	return rr
}

// probe asks the status probe about the profile a device holds.
func (s *e2eServer) probe(t *testing.T, snap recoverySnapshot) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.h.UserStatus(rr, protoRequest(http.MethodPost, "/api/users/status", spaRecoveryProfile(snap.profile)))
	return rr
}

// restoreIdentity stands up server B from A's identity bundle, as
// ops import-identity followed by a RECOVERY_MODE boot does.
func restoreIdentity(t *testing.T, a *e2eServer) *e2eServer {
	t.Helper()
	ctx := context.Background()
	bundle, err := exportFromDB(ctx, a.db, time.Now())
	if err != nil {
		t.Fatalf("exportFromDB: %v", err)
	}
	dbB := newTestDatabase(t, InitDB)
	if _, err := importIntoDB(ctx, dbB, newCryptoService(), e2ePassphrase, "https://test.example", bundle); err != nil {
		t.Fatalf("importIntoDB: %v", err)
	}
	return newE2EServer(t, dbB, true)
}

// A removed account never comes back through recovery, from a backup
// taken before it was removed.
func TestRecoveryE2E_RemovedAccountRefused(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	gina := a.signup(t, "gina")
	snap := a.snapshot(t, gina.id)
	a.deleteAccount(t, gina)

	expectStatus(t, "probe", a.probe(t, snap), http.StatusGone)
	expectStatus(t, "claim", a.claimRequest(t, gina, snap), http.StatusGone)
}

func TestRecoveryE2E_ClaimOwnIdentity(t *testing.T) {
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	alice := a.signup(t, "alice")
	snap := a.snapshot(t, alice.id)

	b := restoreIdentity(t, a)
	b.claim(t, alice, snap)
}

// authed sends msg as u through the real signature-auth middleware.
func (s *e2eServer) authed(t *testing.T, u *e2eUser, method, path string, msg proto.Message, vars map[string]string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	_, _, fp, _ := parseKeyFingerprint(identityID(u.keyID))
	req := signedRequest(t, s.h, method, path, u.id, fp, u.kp.PrivateKey, msg)
	req.Header.Set("X-Syrinx-Device-Id", u.deviceID)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	rr := httptest.NewRecorder()
	s.h.signatureAuthMiddleware("/api")(handler).ServeHTTP(rr, req)
	return rr
}

// authedThrough is authed with the request also passing gate.
func (u *e2eUser) authedThrough(t *testing.T, s *e2eServer, gate func(http.Handler) http.Handler, method, path string, vars map[string]string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	_, _, fp, _ := parseKeyFingerprint(identityID(u.keyID))
	req := signedRequest(t, s.h, method, path, u.id, fp, u.kp.PrivateKey, nil)
	req.Header.Set("X-Syrinx-Device-Id", u.deviceID)
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	s.h.signatureAuthMiddleware("/api")(gate(handler)).ServeHTTP(rr, req)
	return rr
}

func expectStatus(t *testing.T, what string, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code/100 == 2 && want/100 == 2 {
		return
	}
	if rr.Code != want {
		var e pb.Error
		_ = proto.Unmarshal(rr.Body.Bytes(), &e)
		t.Fatalf("%s: %d %s", what, rr.Code, e.GetMessage())
	}
}

// rotate replaces u's key the way the SPA's key rotation does.
func (s *e2eServer) rotate(t *testing.T, u *e2eUser) {
	t.Helper()
	c := s.h.services.crypto
	// Skips the 24h cooldown; a restricted key may rotate at once.
	if _, err := s.db.Exec(`UPDATE public_keys SET restricted = TRUE WHERE id = $1`, u.keyID); err != nil {
		t.Fatal(err)
	}
	kp, err := c.createKeyPair(identityID(u.id).UserID(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	mustSign := func(text, priv string) string {
		sig, err := c.sign(context.Background(), text, priv)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	_, _, oldFP, _ := parseKeyFingerprint(identityID(u.keyID))
	msg := &pb.AddPublicKeyRequest{
		UserId:                  u.id,
		PublicKey:               kp.PublicKey,
		RevokedKeyFingerprint:   oldFP,
		RevokedKeySignature:     mustSign(kp.PublicKey, u.kp.PrivateKey),
		NewKeySignature:         mustSign(kp.PublicKey, kp.PrivateKey),
		RevocationReason:        "rotated",
		RevocationUserSignature: mustSign(string(buildUserRevocationPayload(u.id, u.keyID, "rotated")), u.kp.PrivateKey),
	}
	expectStatus(t, "rotate", s.authed(t, u, http.MethodPost, "/api/keys", msg, nil, s.h.AddPublicKey), http.StatusOK)
	if _, err := s.db.Exec(`UPDATE public_keys SET restricted = FALSE WHERE id = $1`, u.keyID); err != nil {
		t.Fatal(err)
	}
	u.kp = *kp
	u.keyID = string(appendEntity(identityID(u.id), kp.Fingerprint))
}

// post signs a reed; replyTo/echoing are optional canonical reed ids.
func (s *e2eServer) post(t *testing.T, u *e2eUser, replyTo, echoing string, mentions ...string) string {
	t.Helper()
	id := string(appendEntity(identityID(u.id), uuid.Must(uuid.NewV7()).String()))
	sig, err := s.h.services.crypto.sign(context.Background(), "content of "+id, u.kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	var tip string
	_ = s.db.QueryRow(`SELECT id FROM reeds WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, u.id).Scan(&tip)
	msg := &pb.SignReedRequest{Signature: sig, ReedId: id, ReplyingTo: replyTo, Echoing: echoing, PreviousId: tip, Mentions: mentions}
	expectStatus(t, "sign reed", s.authed(t, u, http.MethodPost, "/api/reeds", msg, nil, s.h.SignReed), http.StatusOK)
	return id
}

func (s *e2eServer) follow(t *testing.T, u *e2eUser, target string) {
	t.Helper()
	rr := s.authed(t, u, http.MethodPost, "/api/users/"+target+"/follow", nil, map[string]string{"userID": target}, s.h.FollowUser)
	if rr.Code != http.StatusOK && rr.Code != http.StatusNoContent {
		expectStatus(t, "follow", rr, http.StatusNoContent)
	}
}

// invite creates an invite from u and returns its id and secret.
func (s *e2eServer) invite(t *testing.T, u *e2eUser) (string, string) {
	t.Helper()
	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	id := newTestInviteRecordID(t, u.id)
	body := inviteCreateBody(t, s.h, u.id, u.kp, id, encodeHashHex(hashSecret(secret)), time.Now(), "")
	rr := postCreateInvite(s.h, u.id, body)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		expectStatus(t, "create invite", rr, http.StatusCreated)
	}
	return id, secret
}

// spaReed is a reed as the SPA's reeds store holds it.
type spaReed struct {
	id        string
	userSig   *pb.UserSignature
	serverSig *pb.ServerSignature
	links     *pb.RecoveryReedRequest
}

// device is one user's IndexedDB: their own data plus what they cached.
type device struct {
	user      *e2eUser
	snapshots map[string]recoverySnapshot
	reeds     []spaReed
	following []string
	records   []*pb.RecoveryRecord
	pins      []string
	// recordsFirst reports records, newest first, before anything else.
	recordsFirst bool
}

// deviceOf builds what u's device holds: every user it has seen, the
// reeds it holds, and its follow list.
func (s *e2eServer) deviceOf(t *testing.T, u *e2eUser, known []*e2eUser) device {
	t.Helper()
	ctx := context.Background()
	d := device{user: u, snapshots: map[string]recoverySnapshot{}}
	for _, k := range known {
		d.snapshots[k.id] = s.snapshot(t, k.id)
	}
	// A removed reed's content is dropped; only its removal is kept.
	rows, err := s.db.Query(`
		SELECT reed_id FROM reed_allocations ra WHERE holder_user_id = $1
			AND NOT EXISTS (SELECT 1 FROM reed_removals rm WHERE rm.reed_id = ra.reed_id)
		ORDER BY reed_id
	`, u.id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		att, err := s.h.services.db.GetReedAttestation(ctx, id)
		if err != nil || att == nil {
			t.Fatalf("GetReedAttestation %s: %v", id, err)
		}
		d.reeds = append(d.reeds, spaReed{
			id:        att.ID,
			userSig:   &pb.UserSignature{Id: att.UserKeyID, Armor: att.UserSignature},
			serverSig: pbServerSignature(ServerSignature{ID: att.ServerFingerprint, Armor: att.ServerSignature, SignedAt: att.ServerSignedAt}),
			links:     s.reedLinks(t, att.ID),
		})
	}
	rows, err = s.db.Query(`SELECT following_user_id FROM user_following WHERE user_id = $1 ORDER BY 1`, u.id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		d.following = append(d.following, id)
	}
	rows.Close()
	return d
}

// recover runs the SPA's recovery order for one device: claim self, each
// peer, each reed, follow pages, complete.
func (s *e2eServer) recover(t *testing.T, d device) {
	t.Helper()
	u := d.user
	s.claim(t, u, d.snapshots[u.id])
	if d.recordsFirst {
		s.reportRecords(t, u, slices.Backward(d.records))
	}
	for id, snap := range d.snapshots {
		if id == u.id {
			continue
		}
		msg := &pb.PeerIdentityRequest{Profile: spaRecoveryProfile(snap.profile), Key: spaKeyNest(t, snap)}
		expectStatus(t, "peer "+id, s.authed(t, u, http.MethodPost, "/api/recovery/identity", msg, nil, s.h.ReportPeerIdentity), http.StatusOK)
	}
	for _, r := range d.reeds {
		msg := proto.Clone(r.links).(*pb.RecoveryReedRequest)
		msg.ReedId, msg.UserSignature, msg.ServerSignature = r.id, r.userSig, r.serverSig
		expectStatus(t, "reed "+r.id, s.authed(t, u, http.MethodPost, "/api/recovery/reeds", msg, nil, s.h.ReportReed), http.StatusNoContent)
	}
	if len(d.following) > 0 {
		msg := &pb.RecoveryFollowingRequest{UserIds: d.following}
		expectStatus(t, "following", s.authed(t, u, http.MethodPost, "/api/recovery/following", msg, nil, s.h.ReportFollowing), http.StatusNoContent)
	}
	if !d.recordsFirst {
		s.reportRecords(t, u, slices.All(d.records))
	}
	if len(d.pins) > 0 {
		msg := &pb.RecoveryPinsRequest{ReedIds: d.pins}
		expectStatus(t, "pins", s.authed(t, u, http.MethodPost, "/api/recovery/pins", msg, nil, s.h.ReportPins), http.StatusNoContent)
	}
	expectStatus(t, "complete", s.authed(t, u, http.MethodPost, "/api/recovery/complete", nil, nil, s.h.CompleteImport), http.StatusNoContent)
}

// reportRecords posts records in batches; none may be refused.
func (s *e2eServer) reportRecords(t *testing.T, u *e2eUser, records iter.Seq2[int, *pb.RecoveryRecord]) {
	t.Helper()
	var all []*pb.RecoveryRecord
	for _, r := range records {
		all = append(all, r)
	}
	for batch := range slices.Chunk(all, maxRecoveryRecordsBatch) {
		rr := s.authed(t, u, http.MethodPost, "/api/recovery/records", &pb.RecoveryRecordsRequest{Records: batch}, nil, s.h.ReportRecords)
		expectStatus(t, "records", rr, http.StatusOK)
		var res pb.RecoveryRecordsResponse
		decodeProto(t, rr.Body.Bytes(), &res)
		for i, r := range res.GetResults() {
			if r.GetStatus() == recoveryRecordRefused {
				t.Errorf("%s's record %v refused: %s", u.id, batch[i].GetRecord(), r.GetReason())
			}
		}
	}
}

func queryRows(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			switch x := v.(type) {
			case []byte:
				parts[i] = string(x)
			case time.Time:
				parts[i] = x.UTC().Format(time.RFC3339)
			default:
				parts[i] = fmt.Sprint(x)
			}
		}
		out = append(out, strings.Join(parts, " | "))
	}
	slices.Sort(out)
	return out
}

// Restored tables, keyed by what a row means rather than serial ids. A
// removed reed's own rows go with its content, which no device keeps.
var recoveryRestoredTables = map[string]string{
	"users": `SELECT u.id, u.username, u.role, COALESCE(u.bio, ''), u.created_at, u.active_key_id,
		COALESCE(u.invite_id, ''), us.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at,
		COALESCE(u.suspended_by, '')
		FROM users u JOIN user_signatures us ON us.id = u.user_signature_id
		JOIN server_signatures ss ON ss.id = u.server_signature_id`,
	"removed_users": `SELECT id, COALESCE(username, ''), role, created_at, active_key_id, COALESCE(invite_id, '')
		FROM users WHERE server_signature_id IS NULL`,
	"user_permissions": `SELECT user_id, permission FROM user_permissions`,
	"identities":       `SELECT id, server_id FROM identities`,
	"public_keys": `SELECT pk.id, pk.owner, pk.armor, pk.created_at, COALESCE(pk.predecessor_id, ''), pk.restricted,
		ss.private_key_id, ss.signature, ss.signed_at
		FROM public_keys pk JOIN server_signatures ss ON ss.id = pk.server_signature_id WHERE pk.owner IS NOT NULL`,
	"public_key_revocations": `SELECT r.key_id, COALESCE(r.reason, ''), us.public_key_id, us.signature,
		ss.private_key_id, ss.signature, ss.signed_at, COALESCE(r.successor, ''),
		COALESCE(sus.public_key_id, ''), COALESCE(sus.signature, '')
		FROM public_key_revocations r JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		LEFT JOIN user_signatures sus ON sus.id = r.successor_signature_id`,
	"reeds": `SELECT r.id, r.user_id, r.signed_at, us.public_key_id, us.signature,
		ss.private_key_id, ss.signature, ss.signed_at, COALESCE(r.thread_head, ''), COALESCE(r.thread_index, -1)
		FROM reeds r JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		WHERE r.id NOT IN (SELECT reed_id FROM reed_removals)`,
	"reed_identities": `SELECT id, server_id, author_id FROM reed_identities
		WHERE id NOT IN (SELECT reed_id FROM reed_removals)`,
	"reed_allocations": `SELECT reed_id, holder_user_id FROM reed_allocations
		WHERE reed_id NOT IN (SELECT reed_id FROM reed_removals)`,
	"reed_replies":        `SELECT root_id, reed_id, parent_reed_id, timestamp FROM reed_replies`,
	"reed_echoes":         `SELECT echoing_reed_id, echoed_reed_id, echoing_author_id, echoed_author_id, is_blank, signed_at FROM reed_echoes`,
	"reed_mentions":       `SELECT mentioning_reed_id, mentioned_user_id FROM reed_mentions`,
	"reed_admin_mentions": `SELECT mentioning_reed_id, server_id FROM reed_admin_mentions`,
	"reed_threads": `SELECT t.id, us.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
		FROM reed_threads t JOIN user_signatures us ON us.id = t.user_signature_id
		JOIN server_signatures ss ON ss.id = t.server_signature_id`,
	"reed_removals": `SELECT rm.reed_id, COALESCE(rm.public_key_id, ''), COALESCE(us.signature, ''),
		COALESCE(ss.private_key_id, ''), COALESCE(ss.signature, ''), COALESCE(rm.thread_id, '')
		FROM reed_removals rm LEFT JOIN user_signatures us ON us.id = rm.user_signature_id
		LEFT JOIN server_signatures ss ON ss.id = rm.server_signature_id`,
	"thread_removals": `SELECT tr.thread_id, tr.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at, tr.thread_record
		FROM thread_removals tr JOIN user_signatures us ON us.id = tr.user_signature_id
		JOIN server_signatures ss ON ss.id = tr.server_signature_id`,
	"account_removals": `SELECT ar.user_id, ar.note, COALESCE(ar.public_key_id, ''), COALESCE(us.signature, ''),
		COALESCE(ar.admin_request_id, ''), ss.private_key_id, ss.signature, ss.signed_at
		FROM account_removals ar LEFT JOIN user_signatures us ON us.id = ar.user_signature_id
		JOIN server_signatures ss ON ss.id = ar.server_signature_id`,
	"reeds_liked": `SELECT l.liker_user_id, l.reed_id, l.liker_public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
		FROM reeds_liked l JOIN user_signatures us ON us.id = l.user_signature_id
		JOIN server_signatures ss ON ss.id = l.server_signature_id`,
	"user_blocks": `SELECT b.user_id, b.blocked_user_id, b.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
		FROM user_blocks b JOIN user_signatures us ON us.id = b.user_signature_id
		JOIN server_signatures ss ON ss.id = b.server_signature_id`,
	"user_vouches": `SELECT v.id, v.voucher_user_id, v.voucher_key_id, v.subject_user_id, v.subject_key_id, v.note, v.created_at,
		us.signature, ss.signature, COALESCE(wus.signature, ''), COALESCE(wss.signature, '')
		FROM user_vouches v JOIN user_signatures us ON us.id = v.user_signature_id
		JOIN server_signatures ss ON ss.id = v.server_signature_id
		LEFT JOIN user_signatures wus ON wus.id = v.withdrawal_signature_id
		LEFT JOIN server_signatures wss ON wss.id = v.withdrawal_server_signature_id`,
	"user_vouches_active": `SELECT vouch_id, voucher_user_id, subject_user_id, subject_key_id FROM user_vouches_active`,
	"ledger_entries": `SELECT e.id, COALESCE(e.prev_id, 0), e.kind, COALESCE(e.user_id, ''), COALESCE(e.requester_id, ''),
		COALESCE(e.requested_at::TEXT, ''), e.message, COALESCE(e.content, ''), e.signed_at, ss.private_key_id, ss.signature
		FROM ledger_entries e JOIN server_signatures ss ON ss.id = e.server_signature_id`,
	"ledger_entry_targets": `SELECT entry_id, user_id FROM ledger_entry_targets`,
	"admin_requests": `SELECT r.id, r.action, r.requester_id, r.requester_key_id, us.signature, oss.signature, oss.signed_at,
		COALESCE(r.reason, ''), r.created_at, r.state, COALESCE(r.closed_by, ''), COALESCE(r.closer_key_id, ''),
		COALESCE(cus.signature, ''), ss.signature, r.closed_at
		FROM admin_requests r JOIN user_signatures us ON us.id = r.requester_signature_id
		JOIN server_signatures oss ON oss.id = r.requester_server_signature_id
		LEFT JOIN user_signatures cus ON cus.id = r.closer_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id`,
	"admin_request_targets": `SELECT request_id, user_id, dropped FROM admin_request_targets`,
	"admin_request_content": `SELECT request_id, body FROM admin_request_content`,
	"reed_flags": `SELECT f.reed_id, f.author_id, f.reporter_id, f.note, f.public_key_id, us.signature, f.created_at
		FROM reed_flags f JOIN user_signatures us ON us.id = f.user_signature_id`,
	"pinned_reeds":   `SELECT user_id, reed_id FROM pinned_reeds`,
	"user_following": `SELECT user_id, following_user_id FROM user_following`,
	"user_followers": `SELECT user_id, follower_user_id FROM user_followers`,
	"network_stats":  `SELECT active_users FROM network_stats`,
	"reed_stats": `SELECT reed_id, reply_count, echo_count, like_count, holder_count FROM reed_stats
		WHERE reed_id NOT IN (SELECT reed_id FROM reed_removals)`,
	// A removed account's device never comes back to claim it.
	"user_devices": `SELECT user_id, device_id FROM user_devices WHERE revoked_at IS NULL
		AND user_id NOT IN (SELECT user_id FROM account_removals)`,
}

// recoveryScenario is a live server with history worth rebuilding: admins
// and their requests, an invite, a key rotation, replies, an echo,
// mentions, threads, removals, likes, a block, vouches, a flag and pins.
type recoveryScenario struct {
	a        *e2eServer
	root     *e2eUser
	alice    *e2eUser
	bob      *e2eUser
	carol    *e2eUser
	dave     *e2eUser
	erin     *e2eUser
	everyone []*e2eUser // those who come back
	known    map[string]recoverySnapshot
	flags    []*pb.ReedFlag
}

func newRecoveryScenario(t *testing.T) recoveryScenario {
	t.Helper()
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	root := a.mintRoot(t)
	alice := a.signup(t, "alice")
	inviteID, secret := a.invite(t, alice)
	bob := a.signupInvited(t, "bob", inviteID, secret)
	carol := a.signup(t, "carol")
	dave := a.signup(t, "dave")
	erin := a.signup(t, "erin")
	frank := a.signup(t, "frank")
	gina := a.signup(t, "gina")
	a.rotate(t, alice)
	everyone := []*e2eUser{root, alice, bob, carol, dave, erin}

	first := a.post(t, alice, "", "")
	reply := a.post(t, bob, first, "", alice.id, adminsHandle+"@"+a.h.services.db.GetServerID())
	a.post(t, carol, reply, "")
	echo := a.post(t, carol, "", first)
	a.post(t, alice, "", "")
	gone := a.post(t, bob, "", "")
	a.thread(t, alice, 2)
	removedThread := a.thread(t, alice, 2)
	a.follow(t, bob, alice.id)
	a.follow(t, carol, alice.id)
	a.follow(t, alice, bob.id)
	a.follow(t, alice, carol.id)
	a.follow(t, erin, bob.id)
	// Delivered: every member who comes back holds every reed, as DATA_ACK records it.
	ids := make([]string, len(everyone))
	for i, u := range everyone {
		ids[i] = "'" + u.id + "'"
	}
	if _, err := a.db.Exec(`
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		SELECT r.id, u.id FROM reeds r CROSS JOIN users u WHERE u.id IN (` + strings.Join(ids, ",") + `)
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatal(err)
	}

	// Peers keep the profiles of accounts removed later.
	known := map[string]recoverySnapshot{}
	for _, u := range []*e2eUser{frank, gina} {
		known[u.id] = a.snapshot(t, u.id)
	}

	promote := a.adminRequest(t, root, adminActionPromote, "Helps everyone", permUserKick+","+permUserSuspend, dave)
	a.decide(t, root, promote, adminDecisionApprove)
	suspend := a.adminRequest(t, root, adminActionSuspend, "Spam", "", erin)
	a.decide(t, dave, suspend, adminDecisionApprove)
	kick := a.adminRequest(t, root, adminActionKick, "Abuse", "", frank)
	a.decide(t, dave, kick, adminDecisionApprove)
	rejected := a.adminRequest(t, dave, adminActionSuspend, "Not sure", "", carol)
	a.decide(t, root, rejected, adminDecisionReject)

	a.removeReed(t, bob, gone)
	a.removeThread(t, alice, removedThread)
	a.deleteAccount(t, gina)
	a.like(t, carol, first)
	a.like(t, bob, echo)
	a.block(t, bob, erin)
	a.vouch(t, bob, alice)
	a.vouch(t, carol, alice)
	a.withdrawVouch(t, carol, alice)
	flags := []*pb.ReedFlag{a.flag(t, bob, echo)}
	a.pin(t, alice, first)

	for _, u := range everyone {
		known[u.id] = a.snapshot(t, u.id)
	}
	return recoveryScenario{a: a, root: root, alice: alice, bob: bob, carol: carol, dave: dave, erin: erin,
		everyone: everyone, known: known, flags: flags}
}

// deviceOf builds what u's device holds after the scenario.
func (sc recoveryScenario) deviceOf(t *testing.T, u *e2eUser) device {
	t.Helper()
	d := sc.a.deviceOf(t, u, nil)
	d.snapshots = sc.known
	d.records = sc.a.heldRecords(t, u, sc.flags)
	d.pins = queryRows(t, sc.a.db, `SELECT reed_id FROM pinned_reeds WHERE user_id = '`+u.id+`' ORDER BY pinned_at DESC`)
	return d
}

// diffRestored fails for every restored table that b doesn't rebuild as a had it.
func diffRestored(t *testing.T, a, b *e2eServer, skip ...string) {
	t.Helper()
	for name, q := range recoveryRestoredTables {
		if slices.Contains(skip, name) {
			continue
		}
		want, got := queryRows(t, a.db, q), queryRows(t, b.db, q)
		if !slices.Equal(want, got) {
			t.Errorf("%s differs after recovery\nbefore:\n  %s\nafter:\n  %s", name, strings.Join(want, "\n  "), strings.Join(got, "\n  "))
		}
	}
}

// expectSameProfiles checks b serves each profile byte for byte as a did,
// so clients still verify it.
func expectSameProfiles(t *testing.T, a, b *e2eServer, users []*e2eUser) {
	t.Helper()
	for _, u := range users {
		before, err := a.h.services.db.GetUserProfile(context.Background(), u.id)
		if err != nil {
			t.Fatal(err)
		}
		after, err := b.h.services.db.GetUserProfile(context.Background(), u.id)
		if err != nil || after == nil {
			t.Fatalf("profile %s after recovery: %v", u.id, err)
		}
		if !proto.Equal(pbUser(before), pbUser(after)) {
			t.Errorf("profile %s differs\nbefore: %v\nafter:  %v", u.id, pbUser(before), pbUser(after))
		}
	}
}

func TestRecoveryE2E_RebuildsDatabase(t *testing.T) {
	sc := newRecoveryScenario(t)
	var devices []device
	for _, u := range sc.everyone {
		devices = append(devices, sc.deviceOf(t, u))
	}

	b := restoreIdentity(t, sc.a)
	for _, d := range devices {
		b.recover(t, d)
	}

	for name, q := range recoveryRestoredTables {
		if len(queryRows(t, sc.a.db, q)) == 0 {
			t.Errorf("the scenario leaves %s empty, so it proves nothing", name)
		}
	}
	diffRestored(t, sc.a, b)
	expectSameProfiles(t, sc.a, b, sc.everyone)
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM unclaimed_accounts`); n[0] != "0" {
		t.Errorf("unclaimed_accounts = %s, want 0 once everyone claimed", n[0])
	}
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM pending_recovery_records`); n[0] != "0" {
		t.Errorf("pending_recovery_records = %s, want 0", n[0])
	}
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM pending_follows`); n[0] != "0" {
		t.Errorf("pending_follows = %s, want 0", n[0])
	}

	// Recovered and complete: the probe says so and the API is open again.
	rr := httptest.NewRecorder()
	b.h.UserStatus(rr, protoRequest(http.MethodPost, "/api/users/status", spaRecoveryProfile(devices[0].snapshots[sc.alice.id].profile)))
	expectStatus(t, "status", rr, http.StatusOK)
	gate := recoveryImportGateMiddleware(userIDKey, b.h.services.db.IsOngoing)
	rr = sc.alice.authedThrough(t, b, gate, http.MethodGet, "/api/users/"+sc.bob.id+"/profile", map[string]string{"userID": sc.bob.id}, b.h.GetUserProfile)
	expectStatus(t, "profile after recovery", rr, http.StatusOK)
}

// A user who never comes back is still rebuilt from what peers hold, and
// the order users return in doesn't matter.
func TestRecoveryE2E_AbsentUserRestoredByPeers(t *testing.T) {
	sc := newRecoveryScenario(t)
	var devices []device
	for _, u := range sc.everyone {
		if u != sc.carol {
			devices = append(devices, sc.deviceOf(t, u))
		}
	}

	b := restoreIdentity(t, sc.a)
	for _, d := range devices {
		b.recover(t, d)
	}

	// carol's own device, follows, likes and device binding are gone with her.
	diffRestored(t, sc.a, b, "reed_allocations", "reed_stats", "user_following", "user_followers", "user_devices", "reeds_liked")
	expectSameProfiles(t, sc.a, b, sc.everyone)
	if got := queryRows(t, b.db, `SELECT user_id FROM unclaimed_accounts`); !slices.Equal(got, []string{sc.carol.id}) {
		t.Errorf("unclaimed_accounts = %v, want only carol", got)
	}
	want := queryRows(t, sc.a.db, `SELECT user_id, following_user_id FROM user_following WHERE user_id <> '`+sc.carol.id+`'`)
	if got := queryRows(t, b.db, `SELECT user_id, following_user_id FROM user_following`); !slices.Equal(want, got) {
		t.Errorf("user_following = %v, want %v", got, want)
	}

	// carol comes back later and claims her peer-seeded account.
	b.recover(t, sc.deviceOf(t, sc.carol))
	diffRestored(t, sc.a, b)
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM unclaimed_accounts`); n[0] != "0" {
		t.Errorf("unclaimed_accounts = %s after carol claimed, want 0", n[0])
	}
}

// Records reported before their signers or the ledger entries they follow
// wait, and land once those are restored.
func TestRecoveryE2E_RecordsWaitForWhatTheyName(t *testing.T) {
	sc := newRecoveryScenario(t)
	var devices []device
	for _, u := range sc.everyone {
		d := sc.deviceOf(t, u)
		d.recordsFirst = true
		devices = append(devices, d)
	}

	b := restoreIdentity(t, sc.a)
	b.claim(t, sc.alice, devices[1].snapshots[sc.alice.id])
	b.reportRecords(t, sc.alice, slices.Backward(devices[1].records))
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM pending_recovery_records`); n[0] == "0" {
		t.Fatal("nothing waited, so the scenario doesn't exercise parking")
	}
	for _, d := range devices {
		b.recover(t, d)
	}

	diffRestored(t, sc.a, b)
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM pending_recovery_records`); n[0] != "0" {
		t.Errorf("pending_recovery_records = %s, want 0 once everything they name is back", n[0])
	}
}
