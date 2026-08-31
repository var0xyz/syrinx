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

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/realtime"
	"syrinx/roles"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

func federationWithUID(r *http.Request, uid string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDKey, uid))
}

func openFederationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureFederationTestSchema)
}

func ensureFederationTestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			self BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			base_url TEXT,
			connected BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS base_url TEXT`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS connected BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS key_id VARCHAR(255)`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMP`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS revoked_by VARCHAR(255)`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS revoked_reason TEXT`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS disconnect_requested_at TIMESTAMP`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS disconnect_requested_by VARCHAR(255)`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS disconnect_reason TEXT`,
		`CREATE TABLE IF NOT EXISTS identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(16),
			public_key_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		// users.id IS identities.id now (no separate identity_id column).
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) NOT NULL,
			role VARCHAR(16) NOT NULL DEFAULT 'user',
			bio TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			active_key_id VARCHAR(255),
			user_signature_id INT,
			server_signature_id INT,
			invited_by VARCHAR(255) REFERENCES identities(id)
		)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS active_key_id VARCHAR(255)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS user_signature_id INT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS server_signature_id INT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_by VARCHAR(255) REFERENCES identities(id)`,
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id SERIAL PRIMARY KEY,
			public_key_id VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id SERIAL PRIMARY KEY,
			private_key_id VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL,
			signed_at TIMESTAMP NOT NULL
		)`,
		// GetServerPublicKeyByFingerprint reads this server's own key
		// (id = fingerprint@serverID, owner NULL) from the unified table.
		`CREATE TABLE IF NOT EXISTS public_keys (
			id VARCHAR(255) PRIMARY KEY,
			owner VARCHAR(255) REFERENCES identities(id) ON DELETE CASCADE,
			armor TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			server_signature_id INT NOT NULL UNIQUE REFERENCES server_signatures(id),
			predecessor_id VARCHAR(255) REFERENCES public_keys(id)
		)`,
		// GetPublicKey's revocation EXISTS subquery needs this table even
		// when nothing in a given test ever revokes a key.
		`CREATE TABLE IF NOT EXISTS public_key_revocations (
			revoked_id VARCHAR(255) PRIMARY KEY REFERENCES public_keys(id) ON DELETE CASCADE,
			reason TEXT,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			successor VARCHAR(255) REFERENCES public_keys(id),
			successor_signature_id INT REFERENCES user_signatures(id)
		)`,
		// Minimal shape: GetFederationUserIdentity's account-removal check
		// only needs "does a row exist for this user_id" (see
		// deletion.GetAccountCert / loadAccountCertTx) — no test here
		// exercises an actual removed account through this path, so the
		// signature-id FKs from the real schema are omitted.
		`CREATE TABLE IF NOT EXISTS account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			note VARCHAR(140) NOT NULL DEFAULT '',
			public_key_id VARCHAR(255) NOT NULL DEFAULT '',
			user_signature_id INT,
			server_signature_id INT
		)`,
		// created_by/reviewed_by FK identities(id) now — ListFederationInvitations
		// joins users through identity_id (see services.go).
		`CREATE TABLE IF NOT EXISTS federation_invitation (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL DEFAULT '',
			secret_hash BYTEA NOT NULL,
			fingerprint VARCHAR(255) NOT NULL,
			public_key_armor TEXT NOT NULL DEFAULT '',
			created_by VARCHAR(255) NOT NULL REFERENCES identities(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			accepted_at TIMESTAMPTZ,
			server_id VARCHAR(255) REFERENCES servers(id),
			status VARCHAR(16) NOT NULL DEFAULT 'new'
				CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
			reviewed_by VARCHAR(255) REFERENCES identities(id),
			reviewed_at TIMESTAMPTZ,
			connection_ciphertext TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS federation_attempt (
			id VARCHAR(255) PRIMARY KEY,
			remote_server_id VARCHAR(255) NOT NULL,
			remote_server_name VARCHAR(255) NOT NULL,
			base_url TEXT NOT NULL,
			fingerprint VARCHAR(255) NOT NULL,
			public_key_armor TEXT NOT NULL DEFAULT '',
			invitation_id VARCHAR(255) REFERENCES federation_invitation(id),
			server_id VARCHAR(255) REFERENCES servers(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			status VARCHAR(16) NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'approved', 'rejected')),
			approved_by VARCHAR(255) REFERENCES identities(id),
			approved_at TIMESTAMPTZ,
			rejected_by VARCHAR(255) REFERENCES identities(id),
			rejected_at TIMESTAMPTZ,
			rejected_reason TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS federation_log (
			id VARCHAR(255) PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level VARCHAR(16) NOT NULL CHECK (level IN ('info', 'error')),
			message TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS federation_invitation_log (
			invitation_id VARCHAR(255) NOT NULL REFERENCES federation_invitation(id) ON DELETE CASCADE,
			log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
			PRIMARY KEY (invitation_id, log_id)
		)`,
		`CREATE TABLE IF NOT EXISTS federation_attempt_log (
			attempt_id VARCHAR(255) NOT NULL REFERENCES federation_attempt(id) ON DELETE CASCADE,
			log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
			PRIMARY KEY (attempt_id, log_id)
		)`,
		`CREATE TABLE IF NOT EXISTS federation_server_log (
			server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
			PRIMARY KEY (server_id, log_id)
		)`,
		// Minimal shape for GetForeignHolderServersForAuthor's join — no
		// test here signs an actual reed, so the private-key-fingerprint
		// FK from the real schema is omitted.
		`CREATE TABLE IF NOT EXISTS reed_identities (
			id VARCHAR(255) PRIMARY KEY,
			server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS reeds (
			id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS reed_server_allocations (
			reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
			server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (reed_id, server_id)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// seedFederationUser takes *DataService (not a bare *sql.DB) because it
// needs ds.serverID to mint the matching identities row. Returns the id
// so callers can build wire-facing values without recomputing it.
func seedFederationUser(t *testing.T, ds *DataService, userID, username, role string) string {
	t.Helper()
	identityID := string(identity.CanonicalID(ds.serverID, userID))
	if _, err := ds.db.Exec(`
		INSERT INTO identities (id, server_id) VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, identityID, ds.serverID); err != nil {
		t.Fatal(err)
	}
	// user_signature_id/server_signature_id are NOT NULL-scanned by
	// GetUserProfile (int64, not sql.NullInt64) and it resolves them
	// through signing.GetUserSignature/GetServerSignature — placeholder
	// rows, not real signatures; no test here verifies profile signature
	// contents, only that GetUserProfile/the peer identity endpoint succeed.
	placeholderSig := "placeholder-" + identityID
	var userSigID, serverSigID int64
	if err := ds.db.QueryRow(`
		INSERT INTO user_signatures (public_key_id, signature) VALUES ($1, $2) RETURNING id
	`, placeholderSig, placeholderSig).Scan(&userSigID); err != nil {
		t.Fatal(err)
	}
	if err := ds.db.QueryRow(`
		INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ($1, $2, NOW()) RETURNING id
	`, placeholderSig, placeholderSig).Scan(&serverSigID); err != nil {
		t.Fatal(err)
	}
	_, err := ds.db.Exec(`
		INSERT INTO users (id, username, role, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, role = EXCLUDED.role
	`, identityID, username, role, userSigID, serverSigID)
	if err != nil {
		t.Fatal(err)
	}
	return identityID
}

func testFederationHandlers(t *testing.T) (*Handlers, *DataService, *crypto.KeyPair, *crypto.KeyPair) {
	t.Helper()
	db := openFederationTestDB(t)
	if _, err := db.Exec(`DELETE FROM federation_attempt`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM federation_invitation`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM federation_log`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM servers WHERE self = FALSE`); err != nil {
		t.Fatal(err)
	}

	dataService := NewDataService(db, "test")
	if err := dataService.InitServer(context.Background(), false, "https://test.example"); err != nil {
		t.Fatal(err)
	}

	cryptoSvc := crypto.NewService()
	serverKP, err := cryptoSvc.CreateKeyPair("server-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	remoteKP, err := cryptoSvc.CreateKeyPair("remote-b", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// GetServerPublicKeyByFingerprint reads from public_keys by id =
	// fingerprint@serverID; InitServer registers its own generated key
	// there, not this test's separately-created serverKP, so it needs
	// its own row here (mirrors handlers_signup_gate_test.go).
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

	services := &Services{
		db:     dataService,
		crypto: cryptoSvc,
		log:    NewLoggingService(),
		md:     NewMarkdownService(),
	}
	h := NewHandlers(services, AppConfig{ServerName: "test", APIBaseURL: "https://test.example"}, make(chan realtime.BroadcastMessage, 1), ServerSigningKey{
		Fingerprint: serverKP.Fingerprint,
		Armor:       serverKP.PrivateKey,
	})
	return h, dataService, serverKP, remoteKP
}

func TestCreateFederationInvitation_Admin(t *testing.T) {
	h, ds, serverKP, remoteKP := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin", roles.RoleAdmin)

	body, _ := json.Marshal(federationCreateRequest{
		Name:                 "Acme staging",
		RemotePublicKeyArmor: base64.StdEncoding.EncodeToString([]byte(remoteKP.PublicKey)),
	})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), admin1)
	h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp federationCreateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.InviteID == "" || resp.Status != federationStatusNew || resp.ConnectionString == "" {
		t.Fatalf("resp=%+v", resp)
	}

	inv, err := ds.GetFederationInvitation(context.Background(), resp.InviteID)
	if err != nil || inv == nil {
		t.Fatalf("inv=%v err=%v", inv, err)
	}
	if inv.Status != federationStatusNew || inv.CreatedBy != admin1 || inv.Fingerprint != remoteKP.Fingerprint || inv.Name != "Acme staging" {
		t.Fatalf("row=%+v", inv)
	}

	var storedCiphertext string
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT COALESCE(connection_ciphertext, '') FROM federation_invitation WHERE id = $1`, resp.InviteID,
	).Scan(&storedCiphertext); err != nil {
		t.Fatal(err)
	}
	if storedCiphertext != resp.ConnectionString {
		t.Fatalf("stored ciphertext=%q resp=%q", storedCiphertext, resp.ConnectionString)
	}

	plaintext, err := h.services.crypto.Decrypt(resp.ConnectionString, remoteKP.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	var payload federationConnectionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PublicKeyArmor != serverKP.PublicKey || payload.Fingerprint != serverKP.Fingerprint {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestCreateFederationInvitation_MissingName(t *testing.T) {
	h, _, _, remoteKP := testFederationHandlers(t)
	admin1 := seedFederationUser(t, h.services.db, "admin1", "admin", roles.RoleAdmin)

	body, _ := json.Marshal(federationCreateRequest{RemotePublicKeyArmor: remoteKP.PublicKey})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), admin1)
	h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateFederationInvitation_UserForbidden(t *testing.T) {
	h, _, _, remoteKP := testFederationHandlers(t)
	user1 := seedFederationUser(t, h.services.db, "user1", "alice", roles.RoleUser)

	body, _ := json.Marshal(federationCreateRequest{
		Name:                 "other",
		RemotePublicKeyArmor: remoteKP.PublicKey,
	})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), user1)
	h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestListFederationInvitations_AllAdminsSeeAll(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "other", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", admin1, "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), admin2)
	h.ListFederationInvitations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var list []federationListItemWire
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].CreatedBy != admin1 || list[0].Name != "Partner prod" {
		t.Fatalf("list=%+v", list)
	}
	if list[0].ConnectionString == nil || *list[0].ConnectionString != "cipher-armor" {
		t.Fatalf("connectionString=%v", list[0].ConnectionString)
	}
}

func TestRevokeFederationInvitation_NewOnly(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", admin1, "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/invitations/{id}/revoke", h.RevokeFederationInvitation).Methods(http.MethodPost)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations/inv1/revoke", nil), admin1)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rr.Code, rr.Body.String())
	}

	inv, _ := ds.GetFederationInvitation(context.Background(), "inv1")
	if inv.Status != federationStatusCanceled {
		t.Fatalf("status=%q", inv.Status)
	}
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT reviewed_by, reviewed_at FROM federation_invitation WHERE id = $1`, "inv1",
	).Scan(&reviewedBy, &reviewedAt); err != nil {
		t.Fatal(err)
	}
	// admin1 (seedFederationUser's return value) matches what the
	// handler wrote verbatim.
	if !reviewedBy.Valid || reviewedBy.String != admin1 || !reviewedAt.Valid {
		t.Fatalf("reviewed_by=%v reviewed_at=%v", reviewedBy, reviewedAt)
	}
	var ciphertext sql.NullString
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT connection_ciphertext FROM federation_invitation WHERE id = $1`, "inv1",
	).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext.Valid {
		t.Fatalf("expected ciphertext cleared on revoke, got %q", ciphertext.String)
	}

	rr2 := httptest.NewRecorder()
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations/inv1/revoke", nil), admin1)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("second revoke status=%d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	req3 := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), admin1)
	h.ListFederationInvitations(rr3, req3)
	var list []federationListItemWire
	if err := json.Unmarshal(rr3.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ReviewedBy == nil || *list[0].ReviewedBy != admin1 ||
		list[0].ReviewedAt == nil {
		t.Fatalf("list=%+v", list)
	}
}

func TestMarkFederationInvitationAccepted_ClearsCiphertext(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", admin1, "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	attemptID, err := ds.MarkFederationInvitationAccepted(context.Background(), "inv1", federationPeer{
		ServerID:    "server-b",
		ServerName:  "Server B",
		BaseURL:     "https://b.example",
		Fingerprint: "fp-b",
	}, fixed)
	if err != nil {
		t.Fatal(err)
	}

	// Accepted, but server_id stays NULL — no servers row exists yet, only
	// a pending federation_attempt (see below).
	inv, _ := ds.GetFederationInvitation(context.Background(), "inv1")
	if inv.Status != federationStatusAccepted || inv.AcceptedAt == nil || inv.ServerID != "" {
		t.Fatalf("inv=%+v", inv)
	}
	var ciphertext sql.NullString
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT connection_ciphertext FROM federation_invitation WHERE id = $1`, "inv1",
	).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext.Valid {
		t.Fatalf("expected ciphertext cleared on accept, got %q", ciphertext.String)
	}
	var serverCount int
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE id = $1`, "server-b",
	).Scan(&serverCount); err != nil {
		t.Fatal(err)
	}
	if serverCount != 0 {
		t.Fatalf("expected no servers row before approval, got %d", serverCount)
	}

	// Accepted invitations no longer appear in the pending-invite list — the
	// invitation can't change state anymore, so it now lives under the
	// resulting attempt (and, once approved, the server's own page).
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), admin1)
	h.ListFederationInvitations(rr, req)
	var list []federationListItemWire
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list=%+v, want empty (accepted invitations are excluded)", list)
	}

	// Before approval, GetFederationInvitationForServer finds nothing (no
	// invitation has this server_id yet).
	forServer, err := ds.GetFederationInvitationForServer(context.Background(), "server-b")
	if err != nil {
		t.Fatal(err)
	}
	if forServer != nil {
		t.Fatalf("forServer=%+v, want nil before approval", forServer)
	}

	// Approve (by a different admin) creates the servers row and backfills
	// both the attempt's and the invitation's server_id.
	if _, err := ds.ApproveFederationAttempt(context.Background(), attemptID, admin2, fixed, false, h.countersign); err != nil {
		t.Fatal(err)
	}
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE id = $1`, "server-b",
	).Scan(&serverCount); err != nil {
		t.Fatal(err)
	}
	if serverCount != 1 {
		t.Fatalf("expected servers row after approval, got %d", serverCount)
	}

	forServer, err = ds.GetFederationInvitationForServer(context.Background(), "server-b")
	if err != nil {
		t.Fatal(err)
	}
	if forServer == nil || forServer.ID != "inv1" || forServer.ConnectionCiphertext != "" {
		t.Fatalf("forServer=%+v", forServer)
	}
}

// pendingAttemptFromInvitation creates an invitation as createdBy and
// accepts it, returning the resulting pending federation_attempt id — the
// shared setup for the same-approver tests below.
func pendingAttemptFromInvitation(t *testing.T, ds *DataService, invID, createdBy string, at time.Time) string {
	t.Helper()
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), invID, "Partner", createdBy, "fp-"+invID, "remote-armor", hash, "cipher-armor", at); err != nil {
		t.Fatal(err)
	}
	attemptID, err := ds.MarkFederationInvitationAccepted(context.Background(), invID, federationPeer{
		ServerID:    "server-" + invID,
		ServerName:  "Server " + invID,
		BaseURL:     "https://" + invID + ".example",
		Fingerprint: "fp-" + invID,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	return attemptID
}

func TestApproveFederationAttempt_SameAdminForbidden(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	attemptID := pendingAttemptFromInvitation(t, ds, "inv-same", admin1, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/attempts/{id}/approve", h.ApproveFederationAttempt).Methods(http.MethodPost)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/attempts/"+attemptID+"/approve", nil), admin1)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("approve by inviting admin status=%d body=%s, want 403", rr.Code, rr.Body.String())
	}

	var serverCount int
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM servers WHERE id = $1`, "server-inv-same",
	).Scan(&serverCount); err != nil {
		t.Fatal(err)
	}
	if serverCount != 0 {
		t.Fatalf("expected no servers row after forbidden self-approve, got %d", serverCount)
	}
}

func TestApproveFederationAttempt_DifferentAdminSucceeds(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	attemptID := pendingAttemptFromInvitation(t, ds, "inv-diff", admin1, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/attempts/{id}/approve", h.ApproveFederationAttempt).Methods(http.MethodPost)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/attempts/"+attemptID+"/approve", nil), admin2)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve by different admin status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestApproveFederationAttempt_RootBypassesSelfApprove(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	root := seedFederationUser(t, ds, roles.RootUserID, "root", roles.RoleRoot)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	attemptID := pendingAttemptFromInvitation(t, ds, "inv-root", root, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/attempts/{id}/approve", h.ApproveFederationAttempt).Methods(http.MethodPost)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/attempts/"+attemptID+"/approve", nil), root)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("root self-approve status=%d body=%s, want 200", rr.Code, rr.Body.String())
	}
}

// establishedPeer approves a fresh invitation/attempt pair and returns the
// resulting servers.id — shared setup for the disconnect tests below.
func establishedPeer(t *testing.T, h *Handlers, ds *DataService, invID, createdBy, approvedBy string, callerIsRoot bool, at time.Time) string {
	t.Helper()
	attemptID := pendingAttemptFromInvitation(t, ds, invID, createdBy, at)
	if _, err := ds.ApproveFederationAttempt(context.Background(), attemptID, approvedBy, at, callerIsRoot, h.countersign); err != nil {
		t.Fatal(err)
	}
	return "server-" + invID
}

func TestFederationServerDisconnect_SameAdminConfirmForbidden(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	serverID := establishedPeer(t, h, ds, "inv-dc1", admin1, admin2, false, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/servers/{id}/revoke", h.RequestFederationServerDisconnect).Methods(http.MethodPost)
	api.HandleFunc("/federation/servers/{id}/revoke/confirm", h.ConfirmFederationServerDisconnect).Methods(http.MethodPost)

	body, _ := json.Marshal(map[string]string{"reason": "no longer needed"})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke", bytes.NewReader(body)), admin1)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("request disconnect status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Same admin who requested it tries to confirm — forbidden.
	rr2 := httptest.NewRecorder()
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke/confirm", nil), admin1)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("confirm by requesting admin status=%d body=%s, want 403", rr2.Code, rr2.Body.String())
	}

	var revokedAt sql.NullTime
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT revoked_at FROM servers WHERE id = $1`, serverID,
	).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt.Valid {
		t.Fatalf("expected server to remain connected after forbidden self-confirm, revoked_at=%v", revokedAt)
	}

	// A different admin CAN confirm the same pending request.
	rr3 := httptest.NewRecorder()
	req3 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke/confirm", nil), admin2)
	router.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("confirm by different admin status=%d body=%s", rr3.Code, rr3.Body.String())
	}
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT revoked_at FROM servers WHERE id = $1`, serverID,
	).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if !revokedAt.Valid {
		t.Fatal("expected server to be revoked after confirm by a different admin")
	}
}

func TestFederationServerDisconnect_RootBypassesSelfConfirm(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	root := seedFederationUser(t, ds, roles.RootUserID, "root", roles.RoleRoot)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	serverID := establishedPeer(t, h, ds, "inv-dc2", root, root, true, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/servers/{id}/revoke", h.RequestFederationServerDisconnect).Methods(http.MethodPost)
	api.HandleFunc("/federation/servers/{id}/revoke/confirm", h.ConfirmFederationServerDisconnect).Methods(http.MethodPost)

	body, _ := json.Marshal(map[string]string{"reason": "cleanup"})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke", bytes.NewReader(body)), root)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("request disconnect status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke/confirm", nil), root)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("root self-confirm status=%d body=%s, want 200", rr2.Code, rr2.Body.String())
	}
}

func TestFederationServerDisconnect_CancelClearsRequest(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	serverID := establishedPeer(t, h, ds, "inv-dc3", admin1, admin2, false, fixed)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/servers/{id}/revoke", h.RequestFederationServerDisconnect).Methods(http.MethodPost)
	api.HandleFunc("/federation/servers/{id}/revoke/cancel", h.CancelFederationServerDisconnect).Methods(http.MethodPost)
	api.HandleFunc("/federation/servers/{id}/revoke/confirm", h.ConfirmFederationServerDisconnect).Methods(http.MethodPost)

	body, _ := json.Marshal(map[string]string{"reason": "testing"})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke", bytes.NewReader(body)), admin1)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("request disconnect status=%d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke/cancel", nil), admin2)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", rr2.Code, rr2.Body.String())
	}

	// Confirming after cancel fails — nothing pending anymore.
	rr3 := httptest.NewRecorder()
	req3 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+serverID+"/revoke/confirm", nil), admin2)
	router.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusConflict {
		t.Fatalf("confirm after cancel status=%d body=%s, want 409", rr3.Code, rr3.Body.String())
	}
}

// TestFederationServerSelfRowProtected verifies every disconnect/purge write
// path refuses to touch the self server's own servers row, even when an
// admin (or root, for purge) targets its id directly — self=FALSE is baked
// into each query itself, not just enforced by never offering the self
// server as a target in the UI.
func TestFederationServerSelfRowProtected(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	root := seedFederationUser(t, ds, roles.RootUserID, "root1", roles.RoleRoot)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	selfID := ds.GetServerID()

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/servers/{id}/revoke", h.RequestFederationServerDisconnect).Methods(http.MethodPost)
	api.HandleFunc("/federation/servers/{id}/purge", h.PurgeFederationServer).Methods(http.MethodPost)

	body, _ := json.Marshal(map[string]string{"reason": "testing"})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+selfID+"/revoke", bytes.NewReader(body)), admin1)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("disconnect-request against self server status=%d body=%s, want 404", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/servers/"+selfID+"/purge", nil), root)
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("purge against self server status=%d body=%s, want 404", rr2.Code, rr2.Body.String())
	}

	var stillSelf bool
	if err := ds.db.QueryRow(`SELECT self FROM servers WHERE id = $1`, selfID).Scan(&stillSelf); err != nil {
		t.Fatalf("self server row missing after attempted purge: %v", err)
	}
	if !stillSelf {
		t.Fatal("self server row's self flag was cleared")
	}
}

// TestGetForeignHolderServersForAuthor verifies the account-removal
// peer-discovery query finds every distinct peer holding a copy of any
// reed by the given author, and nothing else (no follower/subscriber
// involvement, no cross-author leakage).
func TestGetForeignHolderServersForAuthor(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	peerA := establishedPeer(t, h, ds, "inv-holder-a", admin1, admin2, false, fixed)
	peerB := establishedPeer(t, h, ds, "inv-holder-b", admin1, admin2, false, fixed)

	author := seedFederationUser(t, ds, "author1", "author1", roles.RoleUser)
	other := seedFederationUser(t, ds, "author2", "author2", roles.RoleUser)

	seedReed := func(reedID, userID string) {
		if _, err := ds.db.Exec(
			`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`,
			reedID, ds.serverID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := ds.db.Exec(
			`INSERT INTO reeds (id, user_id) VALUES ($1, $2)`,
			reedID, userID,
		); err != nil {
			t.Fatal(err)
		}
	}
	reed1 := author + "/reed1"
	reed2 := author + "/reed2"
	otherReed := other + "/reed1"
	seedReed(reed1, author)
	seedReed(reed2, author)
	seedReed(otherReed, other)

	seedHolder := func(reedID, serverID string) {
		if _, err := ds.db.Exec(
			`INSERT INTO reed_server_allocations (reed_id, server_id) VALUES ($1, $2)`,
			reedID, serverID,
		); err != nil {
			t.Fatal(err)
		}
	}
	// peerA holds both of author's reeds (should dedupe to one entry);
	// peerB holds only otherReed (must not appear for author's lookup).
	seedHolder(reed1, peerA)
	seedHolder(reed2, peerA)
	seedHolder(otherReed, peerB)

	got, err := ds.GetForeignHolderServersForAuthor(context.Background(), author)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != peerA {
		t.Fatalf("got %v, want exactly [%s]", got, peerA)
	}
}

func TestGetForeignHolderServersForReed(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	admin1 := seedFederationUser(t, ds, "admin1", "admin1", roles.RoleAdmin)
	admin2 := seedFederationUser(t, ds, "admin2", "admin2", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	peerA := establishedPeer(t, h, ds, "inv-reed-holder-a", admin1, admin2, false, fixed)
	peerB := establishedPeer(t, h, ds, "inv-reed-holder-b", admin1, admin2, false, fixed)

	author := seedFederationUser(t, ds, "reedauthor1", "reedauthor1", roles.RoleUser)

	seedReed := func(reedID, userID string) {
		if _, err := ds.db.Exec(
			`INSERT INTO reed_identities (id, server_id) VALUES ($1, $2)`,
			reedID, ds.serverID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := ds.db.Exec(
			`INSERT INTO reeds (id, user_id) VALUES ($1, $2)`,
			reedID, userID,
		); err != nil {
			t.Fatal(err)
		}
	}
	reed1 := author + "/reed1"
	reed2 := author + "/reed2"
	seedReed(reed1, author)
	seedReed(reed2, author)

	seedHolder := func(reedID, serverID string) {
		if _, err := ds.db.Exec(
			`INSERT INTO reed_server_allocations (reed_id, server_id) VALUES ($1, $2)`,
			reedID, serverID,
		); err != nil {
			t.Fatal(err)
		}
	}
	// Both peers hold reed1; only peerB holds reed2 — a lookup scoped to
	// reed1 must not pick up peerB's unrelated reed2 allocation.
	seedHolder(reed1, peerA)
	seedHolder(reed1, peerB)
	seedHolder(reed2, peerB)

	got, err := ds.GetForeignHolderServersForReed(context.Background(), reed1)
	if err != nil {
		t.Fatal(err)
	}
	gotSet := map[string]bool{}
	for _, s := range got {
		gotSet[s] = true
	}
	if len(got) != 2 || !gotSet[peerA] || !gotSet[peerB] {
		t.Fatalf("got %v, want exactly [%s %s]", got, peerA, peerB)
	}

	got2, err := ds.GetForeignHolderServersForReed(context.Background(), reed2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 || got2[0] != peerB {
		t.Fatalf("got %v, want exactly [%s]", got2, peerB)
	}
}
