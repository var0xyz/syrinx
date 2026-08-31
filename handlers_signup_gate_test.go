//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/invites"
	"syrinx/realtime"
	"syrinx/roles"
)

func newInviteModeHandlers(t *testing.T, db *sql.DB) *Handlers {
	t.Helper()
	return newSignupGateHandlers(t, db, AppConfig{ServerName: "test", SignupMode: "invite"})
}

func newSignupGateHandlers(t *testing.T, db *sql.DB, cfg AppConfig) *Handlers {
	t.Helper()
	dataService := NewDataService(db, "test")
	if err := dataService.InitServer(context.Background(), false, "https://test.example"); err != nil {
		t.Fatal(err)
	}
	cryptoSvc := crypto.NewService()
	serverKP, err := cryptoSvc.CreateKeyPair("test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	services := &Services{
		db:     dataService,
		crypto: cryptoSvc,
		log:    NewLoggingService(),
		md:     NewMarkdownService(),
	}
	if cfg.ServerName == "" {
		cfg.ServerName = "test"
	}
	return NewHandlers(
		services,
		cfg,
		make(chan realtime.BroadcastMessage, 1),
		ServerSigningKey{Fingerprint: serverKP.Fingerprint, Armor: serverKP.PrivateKey},
	)
}

func postCheckUsername(t *testing.T, h *Handlers, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/check-username", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.CheckUsername(rr, req)
	return rr
}

func postSignup(t *testing.T, h *Handlers, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Signup(rr, req)
	return rr
}

func TestCheckUsername_InviteModeRequiresValidInvite(t *testing.T) {
	db := openSignupTestDB(t)
	ctx := t.Context()
	h := newInviteModeHandlers(t, db)

	rr := postCheckUsername(t, h, url.Values{"username": {"bob"}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no invite: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invite required") {
		t.Fatalf("body=%q", rr.Body.String())
	}

	svc := h.services.db
	if _, err := svc.Signup(ctx, signupInput("inviter", "alice", nil)); err != nil {
		t.Fatal(err)
	}

	secret, err := invites.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := invites.HashSecret(secret)
	rawID, err := invites.NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	inviterCanonical := "inviter@" + h.services.db.GetServerID()
	id := inviterCanonical + "/" + rawID
	store := &invites.Store{DB: db, ServerID: h.services.db.GetServerID()}
	if err := store.Insert(ctx, id, inviterCanonical, hash, time.Now().UTC(), roles.RoleUser); err != nil {
		t.Fatal(err)
	}

	rrBad := postCheckUsername(t, h, url.Values{
		"username":     {"bob"},
		"inviteID":     {id},
		"inviteSecret": {"wrong-secret"},
	})
	if rrBad.Code != http.StatusForbidden {
		t.Fatalf("bad secret: status=%d body=%s", rrBad.Code, rrBad.Body.String())
	}
	if !strings.Contains(rrBad.Body.String(), "Invalid or claimed invite") {
		t.Fatalf("body=%q", rrBad.Body.String())
	}

	rrOk := postCheckUsername(t, h, url.Values{
		"username":     {"bob"},
		"inviteID":     {id},
		"inviteSecret": {secret},
	})
	if rrOk.Code != http.StatusOK {
		t.Fatalf("valid invite: status=%d body=%s", rrOk.Code, rrOk.Body.String())
	}
}

// TestCheckUsername_RecoveryModeBlocksEvenWhenOpen guards against username
// sniping: while RECOVERY_MODE is on, the users table may be sparse or
// empty pending peers reporting their evidence back, so nobody may claim a
// username at all — regardless of how permissive SIGNUP_MODE is.
func TestCheckUsername_RecoveryModeBlocksEvenWhenOpen(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test", SignupMode: "open", RecoveryMode: true})

	rr := postCheckUsername(t, h, url.Values{"username": {"bob"}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("recovery mode: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "recovery mode") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

// TestSignup_RecoveryModeBlocksEvenWhenOpen mirrors the CheckUsername case
// for the actual signup endpoint.
func TestSignup_RecoveryModeBlocksEvenWhenOpen(t *testing.T) {
	db := openSignupTestDB(t)
	h := newSignupGateHandlers(t, db, AppConfig{ServerName: "test", SignupMode: "open", RecoveryMode: true})

	rr := postSignup(t, h, url.Values{"username": {"bob"}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("recovery mode: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "recovery mode") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

// signedRequest builds a request signed exactly the way
// signatureAuthMiddleware verifies: method + path + body + timestamp,
// signed with the given private key, base64-encoded into the X-Syrinx-*
// headers.
func signedRequest(t *testing.T, h *Handlers, method, path, userID, fingerprint, privateKeyArmor string, form url.Values) *http.Request {
	t.Helper()
	body := form.Encode()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	canonical := method + " " + path + "\n\n" + body + "\n\n" + timestamp
	sigArmor, err := h.services.crypto.Sign(canonical, privateKeyArmor)
	if err != nil {
		t.Fatalf("sign request: %v", err)
	}
	sigB64 := base64.StdEncoding.EncodeToString([]byte(sigArmor))

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Syrinx-Public-Key-Id", string(identity.AppendEntity(identity.IdentityID(userID), fingerprint)))
	req.Header.Set("X-Syrinx-Signature", sigB64)
	req.Header.Set("X-Syrinx-Signature-Scope", "body")
	req.Header.Set("X-Syrinx-Timestamp", timestamp)
	return req
}

// signedUpUser creates a user with a real keypair (needed to sign requests
// against authenticated endpoints in tests) and returns its keypair. The
// returned KeyPair.Fingerprint stays bare (matching what
// h.services.crypto.CreateKeyPair produces) since callers use it to build
// the canonical X-Syrinx-Public-Key-Id header via signedRequest —
// DataService.Signup itself is given the canonical form, matching what
// handlers.go now does.
func signedUpUser(t *testing.T, h *Handlers, userID, username string) crypto.KeyPair {
	t.Helper()
	kp, err := h.services.crypto.CreateKeyPair(userID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	in := signupInput(userID, username, nil)
	in.Fingerprint = string(identity.AppendEntity(
		identity.CanonicalID(h.services.db.GetServerID(), userID), kp.Fingerprint,
	))
	in.PublicKeyArmor = kp.PublicKey
	in.KeyCreatedAt = time.Now().UTC().Truncate(time.Second)
	if _, err := h.services.db.Signup(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	return *kp
}

// postCheckUsernameForRename drives the request through the real
// signatureAuthMiddleware (not the bare handler) — CheckUsernameForRename
// relies on that middleware having verified the request and populated the
// userID in context, same as it would via the real /api router.
func postCheckUsernameForRename(t *testing.T, h *Handlers, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	handler := h.signatureAuthMiddleware("/api")(http.HandlerFunc(h.CheckUsernameForRename))
	handler.ServeHTTP(rr, req)
	return rr
}

// TestCheckUsernameForRename_NoInviteGate is the regression test for the
// bug: /check-username unconditionally required an invite even for an
// already-signed-in user checking a rename. The dedicated authenticated
// endpoint must never apply that gate, regardless of SignupMode.
func TestCheckUsernameForRename_NoInviteGate(t *testing.T) {
	db := openSignupTestDB(t)
	h := newInviteModeHandlers(t, db)
	kp := signedUpUser(t, h, "alice", "alice")

	req := signedRequest(t, h, http.MethodPost, "/api/users/me/check-username", "alice@"+h.services.db.GetServerID(), kp.Fingerprint, kp.PrivateKey, url.Values{"username": {"bob"}})
	rr := postCheckUsernameForRename(t, h, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCheckUsernameForRename_TakenUsername confirms the availability check
// itself still works (409) on the new endpoint.
func TestCheckUsernameForRename_TakenUsername(t *testing.T) {
	db := openSignupTestDB(t)
	h := newInviteModeHandlers(t, db)
	kp := signedUpUser(t, h, "alice", "alice")
	signedUpUser(t, h, "bob", "bob")

	req := signedRequest(t, h, http.MethodPost, "/api/users/me/check-username", "alice@"+h.services.db.GetServerID(), kp.Fingerprint, kp.PrivateKey, url.Values{"username": {"bob"}})
	rr := postCheckUsernameForRename(t, h, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCheckUsernameForRename_RequiresAuthentication confirms the endpoint
// rejects unsigned requests instead of silently treating them as anonymous
// (unlike /check-username, this one has no legitimate anonymous caller).
func TestCheckUsernameForRename_RequiresAuthentication(t *testing.T) {
	db := openSignupTestDB(t)
	h := newInviteModeHandlers(t, db)
	signedUpUser(t, h, "alice", "alice")

	req := httptest.NewRequest(http.MethodPost, "/api/users/me/check-username", strings.NewReader("username=bob"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := postCheckUsernameForRename(t, h, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("unauthenticated request succeeded: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSignup_HandlerSignsCanonicalUserID drives Signup through the real
// HTTP handler (not svc.Signup, which bypasses the payload-signing code
// entirely) and re-verifies both signed payloads exactly the way the SPA's
// verifyPublicKey/verifyUser do: rebuild the payload using userID@serverID
// (the form every wire response now returns it in) and check it against
// the server signature. Regression test for a bug where the handler signed
// the bare userID form field instead of the canonical id, so the SPA's
// post-signup verification always failed with "verification failed".
func TestSignup_HandlerSignsCanonicalUserID(t *testing.T) {
	db := openSignupTestDB(t)
	dataService := NewDataService(db, "test")
	if err := dataService.InitServer(context.Background(), false, "https://test.example"); err != nil {
		t.Fatal(err)
	}
	cryptoSvc := crypto.NewService()
	serverKP, err := cryptoSvc.CreateKeyPair("test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(
		&Services{db: dataService, crypto: cryptoSvc, log: NewLoggingService(), md: NewMarkdownService()},
		AppConfig{ServerName: "test", SignupMode: "open"},
		make(chan realtime.BroadcastMessage, 1),
		ServerSigningKey{Fingerprint: serverKP.Fingerprint, Armor: serverKP.PrivateKey},
	)
	// GetServerPublicKeyByFingerprint (used to verify the userID reservation
	// signature) reads from public_keys by id = fingerprint@serverID;
	// InitServer registers its own generated key there, not this test's
	// separately-created serverKP, so it needs its own row here. Every
	// public_keys row needs a server_signature_id (NOT NULL); the content
	// doesn't matter for this test's purposes, only that the row exists.
	var serverSigID int64
	if err := db.QueryRow(
		`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, 'sig', now()) RETURNING id`,
		serverKP.Fingerprint,
	).Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO public_keys (id, armor, created_at, server_signature_id) VALUES ($1, $2, now(), $3)`,
		serverKP.Fingerprint+"@"+dataService.GetServerID(), serverKP.PublicKey, serverSigID,
	); err != nil {
		t.Fatal(err)
	}

	userID, err := crypto.NewID()
	if err != nil {
		t.Fatal(err)
	}
	userIDSig, err := h.services.crypto.Sign(userID, h.signingKey.Armor)
	if err != nil {
		t.Fatal(err)
	}

	kp, err := h.services.crypto.CreateKeyPair(userID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pubKeyArmorB64 := base64.StdEncoding.EncodeToString([]byte(kp.PublicKey))
	keySelfSig, err := h.services.crypto.Sign(kp.PublicKey, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	canonicalFingerprint := string(identity.AppendEntity(
		identity.CanonicalID(h.services.db.GetServerID(), userID), kp.Fingerprint,
	))
	identityPayload := identity.BuildUserIdentityPayload("bob", canonicalFingerprint, "")
	userSigArmor, err := h.services.crypto.Sign(string(identityPayload), kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"username":          {"bob"},
		"publicKey":         {pubKeyArmorB64},
		"signature":         {base64.StdEncoding.EncodeToString([]byte(keySelfSig))},
		"userSignature":     {base64.StdEncoding.EncodeToString([]byte(userSigArmor))},
		"userID":            {userID},
		"userIDSignature":   {base64.StdEncoding.EncodeToString([]byte(userIDSig))},
		"userIDFingerprint": {h.signingKey.Fingerprint},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Syrinx-Device-Id", "550e8400-e29b-41d4-a716-446655440000")
	h.Signup(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("signup status=%d body=%s", rr.Code, rr.Body.String())
	}
	var user User
	if err := json.Unmarshal(rr.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	wantID := userID + "@" + h.services.db.GetServerID()
	if user.ID != wantID {
		t.Fatalf("signup response id = %q, want %q", user.ID, wantID)
	}

	key, err := h.services.db.GetPublicKey(t.Context(), canonicalFingerprint)
	if err != nil || key == nil {
		t.Fatalf("GetPublicKey: key=%v err=%v", key, err)
	}
	if key.UserID != wantID {
		t.Fatalf("key.UserID = %q, want %q", key.UserID, wantID)
	}

	// Exactly what verifyPublicKey does client-side: rebuild the payload
	// using the userID this same response returned, then check the server
	// signature against it.
	keyServerFingerprint, keyServerID, ok := identity.ParseIdentityID(identity.IdentityID(key.ServerSignature.ID))
	if !ok {
		t.Fatalf("malformed key server signature id: %s", key.ServerSignature.ID)
	}
	rebuiltKey := identity.BuildPublicKeyPayload(
		keyServerID,
		key.UserID,
		key.ID,
		keyServerFingerprint,
		key.Armor,
		key.ServerSignature.SignedAt,
	)
	keySigArmor, err := base64.StdEncoding.DecodeString(key.ServerSignature.Armor)
	if err != nil {
		t.Fatalf("decode key server signature armor: %v", err)
	}
	if err := h.services.crypto.VerifySignature(string(rebuiltKey), string(keySigArmor), serverKP.PublicKey); err != nil {
		t.Fatalf("public key server signature does not verify against the response's own userID: %v", err)
	}

	// Same check for verifyUser's profile payload rebuild.
	profileServerFingerprint, profileServerID, ok := identity.ParseIdentityID(identity.IdentityID(user.ServerSignature.ID))
	if !ok {
		t.Fatalf("malformed profile server signature id: %s", user.ServerSignature.ID)
	}
	rebuiltProfile := identity.BuildProfilePayload(
		user.ID,
		user.Username,
		user.UserSignature.ID,
		profileServerID,
		profileServerFingerprint,
		user.UserSignature.Armor,
		"",
		user.Role,
		user.Bio,
		user.CreatedAt,
		user.ServerSignature.SignedAt,
	)
	profileSigArmor, err := base64.StdEncoding.DecodeString(user.ServerSignature.Armor)
	if err != nil {
		t.Fatalf("decode profile server signature armor: %v", err)
	}
	if err := h.services.crypto.VerifySignature(string(rebuiltProfile), string(profileSigArmor), serverKP.PublicKey); err != nil {
		t.Fatalf("profile server signature does not verify against the response's own userID: %v", err)
	}
}
