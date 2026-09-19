//go:build !ops

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// newInviteTestHandlers builds *Handlers against openSignupTestDB's schema
// (already has public_keys/invites/users — everything Signup and the
// invite endpoints touch), with SignupMode/MaxInvitesPerUser overridable
// per test. Real crypto service + real signing key, matching how
// newSignupGateHandlers/ripplesTestHandlers set up the rest of this
// package's handler tests — CreateInvite verifies a real signature
// against a real registered public key, there is no injectable
// VerifySignature seam anymore.
func newInviteTestHandlers(t *testing.T, mode string, max int) (*Handlers, *sql.DB) {
	t.Helper()
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test", SignupMode: mode, MaxInvitesPerUser: max})
	return h, db
}

func withInviteUID(r *http.Request, uid string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDKey, uid))
}

// newTestInviteRecordID mints a canonical invite id (creatorID/uuid).
func newTestInviteRecordID(t *testing.T, creatorID string) string {
	t.Helper()
	rawID, err := newInviteID()
	if err != nil {
		t.Fatal(err)
	}
	return creatorID + "/" + rawID
}

// inviteCreateBody builds a real, correctly-signed inviteCreateRequest
// body for creatorID's key kp. id/tokenHashHex, if empty, are minted/
// derived fresh.
func inviteCreateBody(t *testing.T, h *Handlers, creatorID string, kp cryptoKeyPair, id, tokenHashHex string, createdAt time.Time, grantedRole string) *bytes.Buffer {
	t.Helper()
	var secret string
	if tokenHashHex == "" {
		var err error
		secret, err = newInviteSecret()
		if err != nil {
			t.Fatal(err)
		}
		tokenHashHex = encodeHashHex(hashSecret(secret))
	}
	if id == "" {
		id = newTestInviteRecordID(t, creatorID)
	}
	createdAt = createdAt.UTC().Truncate(time.Second)
	// CreateInvite normalizes an empty grantedRole to roleUser before
	// signing/verifying — the signed payload must use the same normalized
	// value or verification fails even though the request itself is valid.
	normalizedRole := grantedRole
	if normalizedRole == "" {
		normalizedRole = roleUser
	}
	// UserSignature.ID travels canonical (fingerprint@serverID) on the
	// wire — matches public_keys.id, which Signup stores canonical too
	// (see signedUpUser). GetPublicKey looks it up as-is, no composition.
	canonicalFingerprint := string(appendEntity(identityID(creatorID), kp.Fingerprint))
	payload := buildInviteUserPayload(h.services.db.GetServerID(), creatorID, id, tokenHashHex, normalizedRole, createdAt)
	sigArmor, err := h.services.crypto.sign(string(payload), kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(inviteCreateRequest{
		ID:          id,
		TokenHash:   tokenHashHex,
		CreatedAt:   createdAt,
		GrantedRole: grantedRole,
		UserSignature: inviteUserSignatureWire{
			ID:    canonicalFingerprint,
			Armor: base64.StdEncoding.EncodeToString([]byte(sigArmor)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(b)
}

func postCreateInvite(h *Handlers, uid string, body *bytes.Buffer) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	if uid != "" {
		req = withInviteUID(req, uid)
	}
	h.CreateInvite(rr, req)
	return rr
}

func TestCreateInvite_Open(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body inviteCreateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" || body.TokenHash == "" || body.ServerSignature.Armor == "" || body.ServerSignature.ID == "" {
		t.Fatalf("empty fields: %+v", body)
	}
	if len(body.TokenHash) != cryptoHashSize*2 {
		t.Fatalf("tokenHash len = %d", len(body.TokenHash))
	}

	rr2 := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr2.Code != http.StatusCreated {
		t.Fatalf("second create status = %d", rr2.Code)
	}
	n, err := h.services.db.countInvitesByCreator(context.Background(), creator)
	if err != nil || n != 2 {
		t.Fatalf("count = %d err=%v", n, err)
	}
}

func TestCreateInvite_Quota(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "invite", 1)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create = %d", rr.Code)
	}

	rr2 := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("quota create = %d want 403", rr2.Code)
	}

	h.cfg.MaxInvitesPerUser = maxInvitesUnlimited
	rr3 := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr3.Code != http.StatusCreated {
		t.Fatalf("unlimited create = %d", rr3.Code)
	}
}

func TestCreateInvite_Closed(t *testing.T) {
	h, db := newInviteTestHandlers(t, "closed", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", fixed, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("create closed = %d want 403", rr.Code)
	}

	id := newTestInviteRecordID(t, creator)
	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.services.db.insertInvite(context.Background(), id, creator, hashSecret(secret), fixed, roleUser, "seed-ufp", "sig"); err != nil {
		t.Fatal(err)
	}
	_ = db
	rrStatus := httptest.NewRecorder()
	statusReq := withInviteUID(httptest.NewRequest(http.MethodGet, "/api/invites/"+id, nil), creator)
	statusReq = mux.SetURLVars(statusReq, map[string]string{"id": id})
	h.InviteStatus(rrStatus, statusReq)
	if rrStatus.Code != http.StatusOK {
		t.Fatalf("status closed = %d", rrStatus.Code)
	}
}

func TestCreateInvite_Unauthenticated(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	rr := postCreateInvite(h, "", inviteCreateBody(t, h, creator, kp, "", "", time.Now(), ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rr.Code)
	}
}

func TestCreateInvite_DuplicateID(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)
	id := newTestInviteRecordID(t, creator)

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, id, "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first = %d", rr.Code)
	}
	rr2 := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, id, "", fixed, ""))
	if rr2.Code != http.StatusConflict {
		t.Fatalf("dup = %d want 409 body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestInviteStatus_ClaimedBy(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	signedUpUser(t, h, "creator", "alice")
	signedUpUser(t, h, "invitee", "bob")
	now := time.Now().UTC().Truncate(time.Second)
	creator := string(canonicalID(h.services.db.GetServerID(), "creator"))

	id := newTestInviteRecordID(t, creator)
	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.services.db.insertInvite(context.Background(), id, creator, hashSecret(secret), now, roleUser, "seed-ufp", "sig"); err != nil {
		t.Fatal(err)
	}
	tx, err := h.services.db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := h.services.db.markInviteClaimed(context.Background(), tx, id, "invitee", now.Add(time.Minute))
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("markInviteClaimed ok=%v err=%v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	statusReq := withInviteUID(httptest.NewRequest(http.MethodGet, "/api/invites/"+id, nil), creator)
	statusReq = mux.SetURLVars(statusReq, map[string]string{"id": id})
	h.InviteStatus(rr, statusReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body inviteStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	wantClaimedBy := string(canonicalID(h.services.db.GetServerID(), "invitee"))
	if body.Status != "claimed" || body.ClaimedBy == nil || *body.ClaimedBy != wantClaimedBy {
		t.Fatalf("unexpected status body: %+v", body)
	}
}

func TestRevokeAndCheckInvite(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)

	secret, err := newInviteSecret()
	if err != nil {
		t.Fatal(err)
	}
	hashHex := encodeHashHex(hashSecret(secret))
	id := newTestInviteRecordID(t, creator)

	rrCreate := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, id, hashHex, fixed, ""))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", rrCreate.Code, rrCreate.Body.String())
	}

	rrCheck := httptest.NewRecorder()
	h.CheckInvite(rrCheck, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrCheck.Code != http.StatusOK || !bytes.Contains(rrCheck.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("check pending: %d %s", rrCheck.Code, rrCheck.Body.String())
	}

	rrRevoke := httptest.NewRecorder()
	revokeReq := withInviteUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+id, nil), creator)
	revokeReq = mux.SetURLVars(revokeReq, map[string]string{"id": id})
	h.DeleteInvite(rrRevoke, revokeReq)
	if rrRevoke.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rrRevoke.Code)
	}

	rrCheck2 := httptest.NewRecorder()
	h.CheckInvite(rrCheck2, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrCheck2.Code != http.StatusOK || !bytes.Contains(rrCheck2.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("check revoked: %d %s", rrCheck2.Code, rrCheck2.Body.String())
	}
}

func TestCheckInvite_Variants(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))
	fixed := time.Now().UTC().Truncate(time.Second)

	rrMissing := httptest.NewRecorder()
	h.CheckInvite(rrMissing, httptest.NewRequest(http.MethodGet, "/api/invites/check", nil))
	if rrMissing.Code != http.StatusBadRequest {
		t.Fatalf("missing = %d", rrMissing.Code)
	}

	rrUnknown := httptest.NewRecorder()
	h.CheckInvite(rrUnknown, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+creator+"/abcdefgh&secret=nope", nil))
	if rrUnknown.Code != http.StatusOK || !bytes.Contains(rrUnknown.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("unknown: %s", rrUnknown.Body.String())
	}

	secret, _ := newInviteSecret()
	id := newTestInviteRecordID(t, creator)
	hashHex := encodeHashHex(hashSecret(secret))
	rrCreate := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, id, hashHex, fixed, ""))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", rrCreate.Code, rrCreate.Body.String())
	}
	rrOk := httptest.NewRecorder()
	h.CheckInvite(rrOk, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrOk.Code != http.StatusOK || !bytes.Contains(rrOk.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("pending check: %s", rrOk.Body.String())
	}

	// Wrong id for valid secret → invalid
	rrWrongID := httptest.NewRecorder()
	h.CheckInvite(rrWrongID, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+creator+"/zzzzzzzz&secret="+secret, nil))
	if !bytes.Contains(rrWrongID.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("wrong id: %s", rrWrongID.Body.String())
	}
}

func TestCreateInvite_UserCannotGrantAdmin(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "u1", "alice")
	creator := string(canonicalID(h.services.db.GetServerID(), "u1"))

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", time.Now(), roleAdmin))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Cannot grant admin role")) {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestCreateInvite_AdminCanGrantAdmin(t *testing.T) {
	h, _ := newInviteTestHandlers(t, "open", maxInvitesUnlimited)
	kp := signedUpUser(t, h, "admin1", "admin")
	creator := string(canonicalID(h.services.db.GetServerID(), "admin1"))
	if _, err := h.services.db.db.Exec(`UPDATE users SET role = $1 WHERE id = $2`, roleAdmin, creator); err != nil {
		t.Fatal(err)
	}

	rr := postCreateInvite(h, creator, inviteCreateBody(t, h, creator, kp, "", "", time.Now(), roleAdmin))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body inviteCreateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.GrantedRole != roleAdmin {
		t.Fatalf("grantedRole = %q want admin", body.GrantedRole)
	}
}
