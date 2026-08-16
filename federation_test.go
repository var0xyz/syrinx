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
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) NOT NULL,
			role VARCHAR(16) NOT NULL DEFAULT 'user'
		)`,
		`CREATE TABLE IF NOT EXISTS public_keys (
			fingerprint VARCHAR(255) PRIMARY KEY,
			armor TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS federation_invitation (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL DEFAULT '',
			secret_hash BYTEA NOT NULL,
			fingerprint VARCHAR(255) NOT NULL REFERENCES public_keys(fingerprint),
			created_by VARCHAR(255) NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			accepted_at TIMESTAMPTZ,
			server_id VARCHAR(255) REFERENCES servers(id),
			status VARCHAR(16) NOT NULL DEFAULT 'new'
				CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
			reviewed_by VARCHAR(255) REFERENCES users(id),
			reviewed_at TIMESTAMPTZ,
			connection_ciphertext TEXT
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
		`CREATE TABLE IF NOT EXISTS federation_server_log (
			server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
			PRIMARY KEY (server_id, log_id)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func seedFederationUser(t *testing.T, db *sql.DB, userID, username, role string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO users (id, username, role) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, role = EXCLUDED.role
	`, userID, username, role)
	if err != nil {
		t.Fatal(err)
	}
}

func testFederationHandlers(t *testing.T) (*Handlers, *DataService, *crypto.KeyPair, *crypto.KeyPair) {
	t.Helper()
	db := openFederationTestDB(t)
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

	services := &Services{
		db:     dataService,
		crypto: cryptoSvc,
		log:    NewLoggingService(),
		md:     NewMarkdownService(),
	}
	if _, err := db.Exec(`
		INSERT INTO public_keys (fingerprint, armor) VALUES ($1, $2)
		ON CONFLICT (fingerprint) DO UPDATE SET armor = EXCLUDED.armor
	`, serverKP.Fingerprint, serverKP.PublicKey); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(services, AppConfig{ServerName: "test", APIBaseURL: "https://test.example"}, make(chan realtime.BroadcastMessage, 1), Key{
		Fingerprint: serverKP.Fingerprint,
		Armor:       serverKP.PrivateKey,
	})
	return h, dataService, serverKP, remoteKP
}

func TestCreateFederationInvitation_Admin(t *testing.T) {
	h, ds, serverKP, remoteKP := testFederationHandlers(t)
	seedFederationUser(t, ds.db, "admin1", "admin", roles.RoleAdmin)

	body, _ := json.Marshal(federationCreateRequest{
		Name:                 "Acme staging",
		RemotePublicKeyArmor: base64.StdEncoding.EncodeToString([]byte(remoteKP.PublicKey)),
	})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), "admin1")
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
	if inv.Status != federationStatusNew || inv.CreatedBy != "admin1" || inv.Fingerprint != remoteKP.Fingerprint || inv.Name != "Acme staging" {
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
	seedFederationUser(t, h.services.db.db, "admin1", "admin", roles.RoleAdmin)

	body, _ := json.Marshal(federationCreateRequest{RemotePublicKeyArmor: remoteKP.PublicKey})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), "admin1")
	h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateFederationInvitation_UserForbidden(t *testing.T) {
	h, _, _, remoteKP := testFederationHandlers(t)
	seedFederationUser(t, h.services.db.db, "user1", "alice", roles.RoleUser)

	body, _ := json.Marshal(federationCreateRequest{
		Name:                 "other",
		RemotePublicKeyArmor: remoteKP.PublicKey,
	})
	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations", bytes.NewReader(body)), "user1")
	h.CreateFederationInvitation(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestListFederationInvitations_AllAdminsSeeAll(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	seedFederationUser(t, ds.db, "admin1", "admin", roles.RoleAdmin)
	seedFederationUser(t, ds.db, "admin2", "other", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", "admin1", "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), "admin2")
	h.ListFederationInvitations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var list []federationListItemWire
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].CreatedBy != "admin1" || list[0].CreatedByUsername != "admin" || list[0].Name != "Partner prod" {
		t.Fatalf("list=%+v", list)
	}
	if list[0].ConnectionString == nil || *list[0].ConnectionString != "cipher-armor" {
		t.Fatalf("connectionString=%v", list[0].ConnectionString)
	}
}

func TestRevokeFederationInvitation_NewOnly(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	seedFederationUser(t, ds.db, "admin1", "admin", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", "admin1", "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/federation/invitations/{id}/revoke", h.RevokeFederationInvitation).Methods(http.MethodPost)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations/inv1/revoke", nil), "admin1")
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
	if !reviewedBy.Valid || reviewedBy.String != "admin1" || !reviewedAt.Valid {
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
	req2 := federationWithUID(httptest.NewRequest(http.MethodPost, "/api/federation/invitations/inv1/revoke", nil), "admin1")
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("second revoke status=%d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	req3 := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), "admin1")
	h.ListFederationInvitations(rr3, req3)
	var list []federationListItemWire
	if err := json.Unmarshal(rr3.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ReviewedBy == nil || *list[0].ReviewedBy != "admin1" ||
		list[0].ReviewedByUsername == nil || *list[0].ReviewedByUsername != "admin" ||
		list[0].ReviewedAt == nil {
		t.Fatalf("list=%+v", list)
	}
}

func TestMarkFederationInvitationAccepted_ClearsCiphertext(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	seedFederationUser(t, ds.db, "admin1", "admin", roles.RoleAdmin)
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hash := crypto.Hash("s")
	if err := ds.InsertFederationInvitation(context.Background(), "inv1", "Partner prod", "admin1", "fp-b", "remote-armor", hash, "cipher-armor", fixed); err != nil {
		t.Fatal(err)
	}

	if err := ds.MarkFederationInvitationAccepted(context.Background(), "inv1", federationPeer{
		ServerID:    "server-b",
		BaseURL:     "https://b.example",
		Fingerprint: "fp-b",
	}, fixed); err != nil {
		t.Fatal(err)
	}

	inv, _ := ds.GetFederationInvitation(context.Background(), "inv1")
	if inv.Status != federationStatusAccepted || inv.AcceptedAt == nil || inv.ServerID != "server-b" {
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
	var connected bool
	if err := ds.db.QueryRowContext(context.Background(),
		`SELECT connected FROM servers WHERE id = $1`, "server-b",
	).Scan(&connected); err != nil {
		t.Fatal(err)
	}
	if !connected {
		t.Fatalf("expected peer server marked connected")
	}

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodGet, "/api/federation/invitations", nil), "admin1")
	h.ListFederationInvitations(rr, req)
	var list []federationListItemWire
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ConnectionString != nil {
		t.Fatalf("list=%+v", list)
	}
}
