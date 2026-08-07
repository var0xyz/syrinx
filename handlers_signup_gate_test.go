//go:build !ops

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	dataService := NewDataService(db, "test")
	if err := dataService.InitServer(context.Background(), false); err != nil {
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
	return NewHandlers(
		services,
		AppConfig{ServerName: "test", SignupMode: "invite"},
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
