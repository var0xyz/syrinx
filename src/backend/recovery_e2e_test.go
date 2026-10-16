//go:build !ops

package main

import (
	"context"
	"database/sql"
	"fmt"
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
	h := s.h
	bare, err := newCryptoID()
	if err != nil {
		t.Fatal(err)
	}
	userIDSig, err := h.services.crypto.sign(bare, h.signingKey.Armor)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := h.services.crypto.createKeyPair(bare, "", "")
	if err != nil {
		t.Fatal(err)
	}
	keySelfSig, err := h.services.crypto.sign(kp.PublicKey, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	self := canonicalID(h.services.db.GetServerID(), bare)
	keyID := string(appendEntity(self, kp.Fingerprint))
	userSig, err := h.services.crypto.sign(string(buildUserIdentityPayload(username, keyID, "")), kp.PrivateKey)
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
	rr := httptest.NewRecorder()
	s.h.IssueChallenge(rr, httptest.NewRequest(http.MethodGet, "/api/recovery/identity/claim", nil))
	var ch pb.ChallengeResponse
	decodeProto(t, rr.Body.Bytes(), &ch)
	sig, err := s.h.services.crypto.sign(ch.GetChallenge(), u.kp.PrivateKey)
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
	if rr.Code != http.StatusOK {
		t.Fatalf("claim %s: %d %s", u.id, rr.Code, errorMessage(t, rr.Body.Bytes()))
	}
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
		sig, err := c.sign(text, priv)
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
	sig, err := s.h.services.crypto.sign("content of "+id, u.kp.PrivateKey)
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
	id, userID string
	userSig    *pb.UserSignature
	serverSig  *pb.ServerSignature
}

// device is one user's IndexedDB: their own data plus what they cached.
type device struct {
	user      *e2eUser
	snapshots map[string]recoverySnapshot
	reeds     []spaReed
	following []string
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
	rows, err := s.db.Query(`SELECT reed_id FROM reed_allocations WHERE holder_user_id = $1 ORDER BY reed_id`, u.id)
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
			userID:    att.UserID,
			userSig:   &pb.UserSignature{Id: att.UserKeyID, Armor: att.UserSignature},
			serverSig: pbServerSignature(ServerSignature{ID: att.ServerFingerprint, Armor: att.ServerSignature, SignedAt: att.ServerSignedAt}),
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
	for id, snap := range d.snapshots {
		if id == u.id {
			continue
		}
		msg := &pb.PeerIdentityRequest{Profile: spaRecoveryProfile(snap.profile), Key: spaKeyNest(t, snap)}
		expectStatus(t, "peer "+id, s.authed(t, u, http.MethodPost, "/api/recovery/identity", msg, nil, s.h.ReportPeerIdentity), http.StatusOK)
	}
	for _, r := range d.reeds {
		_, _, bare, _ := parseKeyFingerprint(identityID(r.id))
		msg := &pb.RecoveryReedRequest{ReedId: bare, AuthorId: r.userID, UserSignature: r.userSig, ServerSignature: r.serverSig}
		expectStatus(t, "reed "+r.id, s.authed(t, u, http.MethodPost, "/api/recovery/reeds", msg, nil, s.h.ReportReed), http.StatusNoContent)
	}
	if len(d.following) > 0 {
		msg := &pb.RecoveryFollowingRequest{UserIds: d.following}
		expectStatus(t, "following", s.authed(t, u, http.MethodPost, "/api/recovery/following", msg, nil, s.h.ReportFollowing), http.StatusNoContent)
	}
	expectStatus(t, "complete", s.authed(t, u, http.MethodPost, "/api/recovery/complete", nil, nil, s.h.CompleteImport), http.StatusNoContent)
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

// Restored tables, keyed by what a row means rather than serial ids.
var recoveryRestoredTables = map[string]string{
	"users": `SELECT u.id, u.username, u.role, COALESCE(u.bio, ''), u.created_at, u.active_key_id,
		COALESCE(u.invite_id, ''), us.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
		FROM users u JOIN user_signatures us ON us.id = u.user_signature_id
		JOIN server_signatures ss ON ss.id = u.server_signature_id`,
	"identities": `SELECT id, server_id FROM identities`,
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
		ss.private_key_id, ss.signature, ss.signed_at
		FROM reeds r JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id`,
	"reed_identities":  `SELECT id, server_id, author_id FROM reed_identities`,
	"reed_allocations": `SELECT reed_id, holder_user_id FROM reed_allocations`,
	"user_following":   `SELECT user_id, following_user_id FROM user_following`,
	"user_followers":   `SELECT user_id, follower_user_id FROM user_followers`,
	"network_stats":    `SELECT active_users FROM network_stats`,
	"reed_stats":       `SELECT reed_id, holder_count FROM reed_stats`,
	"user_devices":     `SELECT user_id, device_id FROM user_devices WHERE revoked_at IS NULL`,
}

// recoveryScenario is a live server with history worth rebuilding:
// an invite, a key rotation, replies, an echo, a mention and follows.
type recoveryScenario struct {
	a                 *e2eServer
	alice, bob, carol *e2eUser
	everyone          []*e2eUser
}

func newRecoveryScenario(t *testing.T) recoveryScenario {
	t.Helper()
	a := newE2EServer(t, newTestDatabase(t, InitDB), false)
	alice := a.signup(t, "alice")
	inviteID, secret := a.invite(t, alice)
	bob := a.signupInvited(t, "bob", inviteID, secret)
	carol := a.signup(t, "carol")
	a.rotate(t, alice)

	first := a.post(t, alice, "", "")
	reply := a.post(t, bob, first, "", alice.id)
	a.post(t, carol, reply, "")
	a.post(t, carol, "", first)
	a.post(t, alice, "", "")
	a.follow(t, bob, alice.id)
	a.follow(t, carol, alice.id)
	a.follow(t, alice, bob.id)
	a.follow(t, alice, carol.id)
	// Delivered: every member holds every reed, as DATA_ACK records it.
	if _, err := a.db.Exec(`
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		SELECT r.id, u.id FROM reeds r CROSS JOIN users u
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatal(err)
	}
	return recoveryScenario{a: a, alice: alice, bob: bob, carol: carol, everyone: []*e2eUser{alice, bob, carol}}
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
		devices = append(devices, sc.a.deviceOf(t, u, sc.everyone))
	}

	b := restoreIdentity(t, sc.a)
	for _, d := range devices {
		b.recover(t, d)
	}

	diffRestored(t, sc.a, b)
	expectSameProfiles(t, sc.a, b, sc.everyone)
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM unclaimed_accounts`); n[0] != "0" {
		t.Errorf("unclaimed_accounts = %s, want 0 once everyone claimed", n[0])
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
	for _, u := range []*e2eUser{sc.bob, sc.alice} {
		devices = append(devices, sc.a.deviceOf(t, u, sc.everyone))
	}

	b := restoreIdentity(t, sc.a)
	for _, d := range devices {
		b.recover(t, d)
	}

	// carol's own device, follows and device binding are gone with her.
	diffRestored(t, sc.a, b, "reed_allocations", "reed_stats", "user_following", "user_followers", "user_devices")
	expectSameProfiles(t, sc.a, b, sc.everyone)
	if got := queryRows(t, b.db, `SELECT user_id FROM unclaimed_accounts`); !slices.Equal(got, []string{sc.carol.id}) {
		t.Errorf("unclaimed_accounts = %v, want only carol", got)
	}
	want := queryRows(t, sc.a.db, `SELECT user_id, following_user_id FROM user_following WHERE user_id <> '`+sc.carol.id+`'`)
	if got := queryRows(t, b.db, `SELECT user_id, following_user_id FROM user_following`); !slices.Equal(want, got) {
		t.Errorf("user_following = %v, want %v", got, want)
	}

	// carol comes back later and claims her peer-seeded account.
	b.recover(t, sc.a.deviceOf(t, sc.carol, sc.everyone))
	diffRestored(t, sc.a, b)
	if n := queryRows(t, b.db, `SELECT COUNT(*) FROM unclaimed_accounts`); n[0] != "0" {
		t.Errorf("unclaimed_accounts = %s after carol claimed, want 0", n[0])
	}
}
