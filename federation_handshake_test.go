package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	api.HandleFunc("/federation/users/{userID}/identity", h.peerAuthMiddleware(h.GetFederationUserIdentity)).Methods(http.MethodGet)
	// peerAuthMiddleware live-fetches the caller's own signing key armor
	// from the caller (fetchPeerServerKeyArmor, GET /server/key) to verify
	// its request signature, so both test servers need to be able to serve
	// their own key back, exactly as main.go registers it.
	api.HandleFunc("/server/key", h.GetServerKey).Methods(http.MethodGet)

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
	// Distinct display names so we can assert each side learns the OTHER's
	// ServerName (not its own id, and not the other's id either) — see
	// federationConnectionPayload/federationConnectRequest's ServerName field.
	a.h.cfg.ServerName = "Alpha"
	b.h.cfg.ServerName = "Bravo"

	aAdmin := seedFederationUser(t, a.ds, "a-admin", "a-admin", roles.RoleAdmin)
	bAdmin := seedFederationUser(t, b.ds, "b-admin", "b-admin", roles.RoleAdmin)
	// Each side's outbound calls to the other's httptest.NewTLSServer must
	// trust its self-signed cert — a real deployment would have a valid
	// public cert. a needs this too: peerAuthMiddleware now live-fetches
	// the peer's own signing key armor (fetchPeerServerKeyArmor) rather
	// than reading a locally-cached copy.
	b.h.federationHTTPClientOverride = a.srv.Client()
	a.h.federationHTTPClientOverride = b.srv.Client()

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

	// a's invitation should now be accepted — but server_id stays NULL until
	// a federation_attempt against it is APPROVED (not yet).
	inv, err := a.ds.GetFederationInvitation(context.Background(), inviteID)
	if err != nil || inv == nil {
		t.Fatalf("inv=%v err=%v", inv, err)
	}
	if inv.Status != federationStatusAccepted || inv.AcceptedAt == nil || inv.ServerID != "" {
		t.Fatalf("a invitation=%+v", inv)
	}

	// No servers row exists yet on either side — servers only gets a row on
	// approval (ApproveFederationAttempt).
	var aServerCount, bServerCount int
	if err := a.ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE self = FALSE`,
	).Scan(&aServerCount); err != nil {
		t.Fatal(err)
	}
	if aServerCount != 0 {
		t.Fatalf("expected no servers row on a before approval, got %d", aServerCount)
	}
	if err := b.ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE self = FALSE`,
	).Scan(&bServerCount); err != nil {
		t.Fatal(err)
	}
	if bServerCount != 0 {
		t.Fatalf("expected no servers row on b before approval, got %d", bServerCount)
	}

	// a should have a pending federation_attempt for b, with b's real
	// display name ("Bravo"), tied to the invitation.
	aAttempts, err := a.ds.ListFederationAttempts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(aAttempts) != 1 {
		t.Fatalf("a attempts=%+v, want 1", aAttempts)
	}
	aAttempt := aAttempts[0]
	if aAttempt.Status != "pending" || aAttempt.RemoteServerName != "Bravo" || aAttempt.InvitationID != inviteID {
		t.Fatalf("a attempt=%+v", aAttempt)
	}

	// b should independently have a pending attempt for a, with a's real
	// display name ("Alpha"), no local invitation (b is the responder).
	bAttempts, err := b.ds.ListFederationAttempts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bAttempts) != 1 {
		t.Fatalf("b attempts=%+v, want 1", bAttempts)
	}
	bAttempt := bAttempts[0]
	if bAttempt.Status != "pending" || bAttempt.RemoteServerName != "Alpha" || bAttempt.InvitationID != "" {
		t.Fatalf("b attempt=%+v", bAttempt)
	}

	// Approving a's attempt for b creates the servers row, sets server_id
	// on both the attempt and the (approved) invitation.
	if err := a.ds.ApproveFederationAttempt(context.Background(), aAttempt.ID, aAdmin, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	approvedInv, err := a.ds.GetFederationInvitation(context.Background(), inviteID)
	if err != nil || approvedInv == nil {
		t.Fatalf("approvedInv=%v err=%v", approvedInv, err)
	}
	if approvedInv.Status != federationStatusApproved || approvedInv.ServerID != aAttempt.RemoteServerID {
		t.Fatalf("approved invitation=%+v, want server_id=%q", approvedInv, aAttempt.RemoteServerID)
	}
	var aConnected bool
	var aPeerName string
	if err := a.ds.db.QueryRowContext(context.Background(),
		`SELECT connected, name FROM servers WHERE id = $1`, approvedInv.ServerID,
	).Scan(&aConnected, &aPeerName); err != nil {
		t.Fatal(err)
	}
	if !aConnected || aPeerName != "Bravo" {
		t.Fatalf("a's approved server: connected=%v name=%q", aConnected, aPeerName)
	}

	// Approve b's side too, so both instances have an established peer —
	// mirrors a real bidirectional federation link.
	if err := b.ds.ApproveFederationAttempt(context.Background(), bAttempt.ID, bAdmin, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// b resolves a's admin user through the peer-authenticated IdP endpoint
	// (specs/federation/04) — proves peerAuthMiddleware end to end: correct
	// signature + pinned fingerprint against a's own establishment.
	identReq, err := http.NewRequest(http.MethodGet, a.srv.URL+"/api/federation/users/"+aAdmin+"/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.h.setFederationPeerAuthHeaders(identReq); err != nil {
		t.Fatal(err)
	}
	identResp, err := a.srv.Client().Do(identReq)
	if err != nil {
		t.Fatal(err)
	}
	defer identResp.Body.Close()
	if identResp.StatusCode != http.StatusOK {
		t.Fatalf("identity status=%d", identResp.StatusCode)
	}

	// Wrong fingerprint (claims to be b, signs with a throwaway key) is
	// rejected, not just "signature invalid" — VerifyFederationPeer fails
	// closed on any mismatch.
	strangerKP, err := crypto.NewService().CreateKeyPair("stranger", "", "")
	if err != nil {
		t.Fatal(err)
	}
	badReq, err := http.NewRequest(http.MethodGet, a.srv.URL+"/api/federation/users/"+aAdmin+"/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	badPayload := identity.BuildFederationPeerRequestPayload(bAttempt.RemoteServerID, http.MethodGet, badReq.URL.Path, timestamp)
	badSigArmor, err := crypto.NewService().Sign(string(badPayload), strangerKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	badReq.Header.Set("X-Syrinx-Federation-Server-Id", bAttempt.RemoteServerID)
	badReq.Header.Set("X-Syrinx-Federation-Fingerprint", strangerKP.Fingerprint)
	badReq.Header.Set("X-Syrinx-Federation-Signature", base64.StdEncoding.EncodeToString([]byte(badSigArmor)))
	badReq.Header.Set("X-Syrinx-Federation-Timestamp", timestamp)
	badResp, err := a.srv.Client().Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unpinned fingerprint, got %d", badResp.StatusCode)
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
	if _, err := a.ds.MarkFederationInvitationAccepted(context.Background(), "inv1", federationPeer{
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
