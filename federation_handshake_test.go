package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/roles"

	"github.com/gorilla/mux"
	"github.com/tooxie/env"
)

// federationServer bundles a Handlers instance behind an httptest.Server so
// two independently-keyed "servers" (A and B) can talk real HTTP for the
// connect handshake, exactly as they would over the network.
type federationServer struct {
	h      *Handlers
	ds     *DataService
	kp     *crypto.KeyPair
	srv    *httptest.Server
	router *mux.Router
}

func newFederationServer(t *testing.T, name string) *federationServer {
	t.Helper()
	h, ds, kp, _ := testFederationHandlers(t)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/invitations", h.CreateFederationInvitation).Methods(http.MethodPost)
	api.HandleFunc("/federation/invitations", h.ListFederationInvitations).Methods(http.MethodGet)
	api.HandleFunc("/federation/invitations/{id}/revoke", h.RevokeFederationInvitation).Methods(http.MethodPost)
	api.HandleFunc("/federation/attempt", h.OutgoingFederationAttempt).Methods(http.MethodPost)
	api.HandleFunc("/federation/connect/{id}", h.IncomingFederationAttempt).Methods(http.MethodPost)

	// TLS: the connect/attempt handlers reject non-https baseUrls, so the
	// initiator side must be served over TLS even in tests.
	srv := httptest.NewTLSServer(router)
	t.Cleanup(srv.Close)

	// Point federationBaseURL at this server's real listener address.
	h.cfg.APIBaseURL = env.HTTPURL(srv.URL)

	return &federationServer{h: h, ds: ds, kp: kp, srv: srv, router: router}
}

// createInvitationEncryptedTo mints an invitation on fs (acting as the
// initiator) addressed to remoteKP, returning the invite id and the
// resulting connection string (as an admin on fs would receive it).
func createInvitationEncryptedTo(t *testing.T, fs *federationServer, adminID string, remoteKP *crypto.KeyPair) (inviteID, connectionString string) {
	t.Helper()
	body, _ := json.Marshal(federationCreateRequest{
		Name:                 "peer",
		RemotePublicKeyArmor: base64.StdEncoding.EncodeToString([]byte(remoteKP.PublicKey)),
	})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), adminID)
	fs.h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create invitation status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp federationCreateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.InviteID, resp.ConnectionString
}

func TestFederationHandshake_FullRoundTrip(t *testing.T) {
	a := newFederationServer(t, "server-a")
	b := newFederationServer(t, "server-b")

	aAdmin := seedFederationUser(t, a.ds, "a-admin", "a-admin", roles.RoleAdmin)
	bAdmin := seedFederationUser(t, b.ds, "b-admin", "b-admin", roles.RoleAdmin)
	// b's outbound call to a's httptest.NewTLSServer must trust its
	// self-signed cert — a real deployment would have a valid public cert.
	b.h.federationHTTPClientOverride = a.srv.Client()

	// a's admin creates an invitation addressed to b's real key; the
	// connection payload's signed baseUrl points at a's actual test server
	// (see createInvitationEncryptedTo) so b can call back to it for real.
	inviteID, connectionString := createInvitationEncryptedTo(t, a, aAdmin, b.kp)

	// b's admin pastes the connection string.
	attemptBody, _ := json.Marshal(federationAttemptRequest{ConnectionString: connectionString})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/attempt", bytes.NewReader(attemptBody)), bAdmin)
	b.h.OutgoingFederationAttempt(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("attempt status=%d body=%s", rr.Code, rr.Body.String())
	}
	var attemptResp federationAttemptResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &attemptResp); err != nil {
		t.Fatal(err)
	}
	if attemptResp.Status != federationStatusAccepted || attemptResp.ServerID == "" {
		t.Fatalf("attemptResp=%+v", attemptResp)
	}

	// a's invitation should now be accepted, with server_id set.
	inv, err := a.ds.GetFederationInvitation(context.Background(), inviteID)
	if err != nil || inv == nil {
		t.Fatalf("inv=%v err=%v", inv, err)
	}
	if inv.Status != federationStatusAccepted || inv.AcceptedAt == nil || inv.ServerID == "" {
		t.Fatalf("a invitation=%+v", inv)
	}

	// a should have recorded b as a peer server — handshake verified, but
	// connected stays FALSE until a second admin approves (spec 03, not
	// yet built); the handshake alone must not flip it.
	var aConnected bool
	if err := a.ds.db.QueryRowContext(context.Background(),
		`SELECT connected FROM servers WHERE id = $1`, inv.ServerID,
	).Scan(&aConnected); err != nil {
		t.Fatal(err)
	}
	if aConnected {
		t.Fatalf("expected peer server NOT connected on a (awaiting approval)")
	}

	// b should independently have a as a peer server too, also unapproved.
	var bServerCount int
	if err := b.ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE self = FALSE AND connected = FALSE`,
	).Scan(&bServerCount); err != nil {
		t.Fatal(err)
	}
	if bServerCount != 1 {
		t.Fatalf("expected 1 unapproved peer server on b, got %d", bServerCount)
	}
}

func TestIncomingFederationAttempt_WrongSecret(t *testing.T) {
	a := newFederationServer(t, "server-a")
	aAdmin := seedFederationUser(t, a.ds, "a-admin", "a-admin", roles.RoleAdmin)

	remoteKP, err := crypto.NewService().CreateKeyPair("remote", "", "")
	if err != nil {
		t.Fatal(err)
	}
	inviteID, _ := createInvitationEncryptedTo(t, a, aAdmin, remoteKP)

	signBytes := identity.BuildFederationConnectPayload(inviteID, "server-b", a.srv.URL, remoteKP.Fingerprint)
	sigArmor, err := a.h.services.crypto.Sign(string(signBytes), remoteKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	connectBody, _ := json.Marshal(federationConnectRequest{
		ServerID:    "server-b",
		BaseURL:     a.srv.URL,
		Fingerprint: remoteKP.Fingerprint,
		Signature:   base64.StdEncoding.EncodeToString([]byte(sigArmor)),
		Secret:      "wrong-secret",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/federation/connect/"+inviteID, bytes.NewReader(connectBody))
	req = mux.SetURLVars(req, map[string]string{"id": inviteID})
	a.h.IncomingFederationAttempt(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	inv, _ := a.ds.GetFederationInvitation(context.Background(), inviteID)
	if inv.Status != federationStatusNew {
		t.Fatalf("expected invitation to stay new, got %q", inv.Status)
	}
}

func TestIncomingFederationAttempt_ReplayNotNew(t *testing.T) {
	a := newFederationServer(t, "server-a")
	aAdmin := seedFederationUser(t, a.ds, "a-admin", "a-admin", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := a.ds.InsertFederationInvitation(context.Background(), "inv1", "peer", aAdmin, "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}
	if err := a.ds.MarkFederationInvitationAccepted(context.Background(), "inv1", federationPeer{
		ServerID:    "server-b",
		BaseURL:     "https://b.example",
		Fingerprint: "fp-b",
	}, fixed); err != nil {
		t.Fatal(err)
	}

	connectBody, _ := json.Marshal(federationConnectRequest{
		ServerID:    "server-b",
		BaseURL:     "https://b.example",
		Fingerprint: "fp-b",
		Signature:   base64.StdEncoding.EncodeToString([]byte("irrelevant")),
		Secret:      "s",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/federation/connect/inv1", bytes.NewReader(connectBody))
	req = mux.SetURLVars(req, map[string]string{"id": "inv1"})
	a.h.IncomingFederationAttempt(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestOutgoingFederationAttempt_InvalidInitiatorSignature(t *testing.T) {
	a := newFederationServer(t, "server-a")
	b := newFederationServer(t, "server-b")
	seedFederationUser(t, a.ds, "a-admin", "a-admin", roles.RoleAdmin)
	bAdmin := seedFederationUser(t, b.ds, "b-admin", "b-admin", roles.RoleAdmin)

	// Build a connection payload by hand with a bogus signature — as if
	// the invite was tampered with or corrupted in transit.
	payload := federationConnectionPayload{
		InviteID:       "inv1",
		ServerID:       "server-a",
		BaseURL:        a.srv.URL,
		Fingerprint:    a.kp.Fingerprint,
		PublicKeyArmor: a.kp.PublicKey,
		Signature:      base64.StdEncoding.EncodeToString([]byte("not-a-real-signature")),
		Secret:         "s",
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	connectionString, err := b.h.services.crypto.Encrypt(plaintext, b.kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	attemptBody, _ := json.Marshal(federationAttemptRequest{ConnectionString: connectionString})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/attempt", bytes.NewReader(attemptBody)), bAdmin)
	b.h.OutgoingFederationAttempt(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	// No peer server row should have been created on b — signature
	// verification fails before RedeemFederationInvitation ever runs.
	var count int
	if err := b.ds.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM servers WHERE self = FALSE`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no peer server rows on b, got %d", count)
	}
}
