//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"syrinx/crypto"
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
		Key{Fingerprint: serverKP.Fingerprint, Armor: serverKP.PrivateKey},
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
	id, err := invites.NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	store := &invites.Store{DB: db}
	if err := store.Insert(ctx, id, "inviter", hash, time.Now().UTC(), roles.RoleUser); err != nil {
		t.Fatal(err)
	}

	rrBad := postCheckUsername(t, h, url.Values{
		"username":          {"bob"},
		"inviteID":          {id},
		"inviteCreatorID":   {"inviter"},
		"inviteSecret":      {"wrong-secret"},
	})
	if rrBad.Code != http.StatusForbidden {
		t.Fatalf("bad secret: status=%d body=%s", rrBad.Code, rrBad.Body.String())
	}
	if !strings.Contains(rrBad.Body.String(), "Invalid or claimed invite") {
		t.Fatalf("body=%q", rrBad.Body.String())
	}

	rrOk := postCheckUsername(t, h, url.Values{
		"username":          {"bob"},
		"inviteID":          {id},
		"inviteCreatorID":   {"inviter"},
		"inviteSecret":      {secret},
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
	req.Header.Set("X-Syrinx-User-Id", userID)
	req.Header.Set("X-Syrinx-Fingerprint", fingerprint)
	req.Header.Set("X-Syrinx-Signature", sigB64)
	req.Header.Set("X-Syrinx-Signature-Scope", "body")
	req.Header.Set("X-Syrinx-Timestamp", timestamp)
	return req
}

// signedUpUser creates a user with a real keypair (needed to sign requests
// against authenticated endpoints in tests) and returns its keypair.
func signedUpUser(t *testing.T, h *Handlers, userID, username string) crypto.KeyPair {
	t.Helper()
	kp, err := h.services.crypto.CreateKeyPair(userID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	in := signupInput(userID, username, nil)
	in.Fingerprint = kp.Fingerprint
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

	req := signedRequest(t, h, http.MethodPost, "/api/users/me/check-username", "alice", kp.Fingerprint, kp.PrivateKey, url.Values{"username": {"bob"}})
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

	req := signedRequest(t, h, http.MethodPost, "/api/users/me/check-username", "alice", kp.Fingerprint, kp.PrivateKey, url.Values{"username": {"bob"}})
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
