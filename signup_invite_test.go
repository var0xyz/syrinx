//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"syrinx/invites"
	"syrinx/roles"

	_ "github.com/lib/pq"
)

func openSignupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newTestDatabase(t, ensureSignupInviteSchema)
}

func ensureSignupInviteSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (
			id VARCHAR(16) UNIQUE,
			name VARCHAR(255) PRIMARY KEY,
			self BOOLEAN NOT NULL DEFAULT FALSE,
			signing_key VARCHAR(255),
			identity_backup_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			base_url TEXT,
			connected BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`CREATE TABLE IF NOT EXISTS user_signatures (
			id SERIAL PRIMARY KEY,
			fingerprint VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_signatures (
			id SERIAL PRIMARY KEY,
			fingerprint VARCHAR(255) NOT NULL,
			signature TEXT NOT NULL,
			signed_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS public_keys (
			fingerprint VARCHAR(255) PRIMARY KEY,
			armor TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`DROP TABLE IF EXISTS user_devices CASCADE`,
		`DROP TABLE IF EXISTS user_following CASCADE`,
		`DROP TABLE IF EXISTS user_followers CASCADE`,
		`DROP TABLE IF EXISTS invites CASCADE`,
		`DROP TABLE IF EXISTS user_keys CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS identities CASCADE`,
		// identities is the FK target for "a user" (see db.go) — Signup
		// mints a row here before the users row, same transaction.
		`CREATE TABLE identities (
			id VARCHAR(255) PRIMARY KEY,
			remote_user_id VARCHAR(255) NOT NULL,
			server_id VARCHAR(16),
			public_key_fingerprint VARCHAR(255),
			verified BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (remote_user_id, server_id)
		)`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) UNIQUE NOT NULL,
			role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (role IN ('root', 'admin', 'user')),
			bio TEXT,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			invited_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE user_keys (
			fingerprint VARCHAR(255) PRIMARY KEY,
			owner VARCHAR(255) NOT NULL REFERENCES identities(id),
			armor TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			predecessor_signature TEXT,
			predecessor_fingerprint VARCHAR(255)
		)`,
		`CREATE TABLE user_key_revocations (
			user_fingerprint VARCHAR(255) NOT NULL REFERENCES user_keys(fingerprint),
			owner VARCHAR(255) NOT NULL REFERENCES identities(id),
			PRIMARY KEY (owner, user_fingerprint)
		)`,
		`CREATE TABLE IF NOT EXISTS account_removals (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)
		)`,
		`CREATE TABLE user_devices (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			linked_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ NULL,
			PRIMARY KEY (user_id, device_id, linked_at)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS user_devices_one_active_per_user
			ON user_devices (user_id) WHERE revoked_at IS NULL`,
		`CREATE TABLE invites (
			created_by VARCHAR(255) NOT NULL REFERENCES identities(id),
			id VARCHAR(255) NOT NULL,
			token_hash BYTEA NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			claimed_at TIMESTAMPTZ,
			claimed_by VARCHAR(255) REFERENCES identities(id),
			revoked_at TIMESTAMPTZ,
			granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
				CHECK (granted_role IN ('admin', 'user')),
			PRIMARY KEY (created_by, id)
		)`,
		`CREATE TABLE user_following (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			following_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, following_user_id)
		)`,
		`CREATE TABLE user_followers (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, follower_user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS network_stats (
			id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
			active_users INT NOT NULL DEFAULT 0
		)`,
		`INSERT INTO network_stats (id, active_users) VALUES (TRUE, 0)
			ON CONFLICT (id) DO NOTHING`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func signupInput(userID, username string, inv *invites.Invite) SignupInput {
	now := time.Now().UTC().Truncate(time.Second)
	return SignupInput{
		UserID:           userID,
		Username:         username,
		PublicKeyArmor:   "armor-" + userID,
		Fingerprint:      "fp-" + userID,
		KeyCreatedAt:     now,
		UserSignatureB64: "usig-" + userID,
		MemberSince:      now,
		ProfileSignature: ServerSignature{
			Fingerprint: "sfp",
			Armor:       "psig-" + userID,
			SignedAt:    now,
		},
		PublicKeySignature: ServerSignature{
			Fingerprint: "sfp",
			Armor:       "ksig-" + userID,
			SignedAt:    now,
		},
		Invite: inv,
	}
}

func queryUserRole(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
		t.Fatalf("query role for %q: %v", userID, err)
	}
	return role
}

func TestSignup_OpenNoInvite(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")

	user, err := svc.Signup(context.Background(), signupInput("u1", "alice", nil))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy != nil {
		t.Fatalf("invitedBy = %+v, want nil", user.InvitedBy)
	}
	if role := queryUserRole(t, db, "u1@srv"); role != roles.RoleUser {
		t.Fatalf("role = %q want %q", role, roles.RoleUser)
	}
}

func TestSignup_ConsumeInvite(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")
	ctx := context.Background()

	if _, err := svc.Signup(context.Background(), signupInput("inviter", "alice", nil)); err != nil {
		t.Fatal(err)
	}

	raw, err := invites.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := invites.HashSecret(raw)
	id, err := invites.NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	store := &invites.Store{DB: db, ServerID: "srv"}
	if err := store.Insert(ctx, id, "inviter@srv", hash, time.Now().UTC(), roles.RoleUser); err != nil {
		t.Fatal(err)
	}
	invRow, err := store.GetByTokenHash(ctx, hash)
	if err != nil || invRow == nil {
		t.Fatalf("load invite: err=%v inv=%v", err, invRow)
	}

	user, err := svc.Signup(context.Background(), signupInput("invitee", "bob", invRow))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy == nil || user.InvitedBy.ID != "inviter@srv" || user.InvitedBy.Username != "alice" {
		t.Fatalf("invitedBy = %+v", user.InvitedBy)
	}
	if role := queryUserRole(t, db, "invitee@srv"); role != roles.RoleUser {
		t.Fatalf("invitee role = %q want %q", role, roles.RoleUser)
	}

	inv, err := store.GetByTokenHash(ctx, hash)
	if err != nil || inv == nil || inv.Status() != "claimed" {
		t.Fatalf("invite status = %+v err=%v", inv, err)
	}

	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM user_following`).Scan(&n)
	if n != 0 {
		t.Fatalf("follow edges = %d, want 0", n)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM user_followers`).Scan(&n)
	if n != 0 {
		t.Fatalf("follower edges = %d, want 0", n)
	}

	_, err = svc.Signup(context.Background(), signupInput("other", "carol", invRow))
	if !errors.Is(err, invites.ErrInvalidInvite) {
		t.Fatalf("reuse = %v, want ErrInvalidInvite", err)
	}
}

func TestSignup_OpenValidToken(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")
	ctx := context.Background()

	if _, err := svc.Signup(context.Background(), signupInput("inviter", "alice", nil)); err != nil {
		t.Fatal(err)
	}
	raw, err := invites.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := invites.HashSecret(raw)
	id, err := invites.NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	store := &invites.Store{DB: db, ServerID: "srv"}
	if err := store.Insert(ctx, id, "inviter@srv", hash, time.Now().UTC(), roles.RoleUser); err != nil {
		t.Fatal(err)
	}
	invRow, err := store.GetByTokenHash(ctx, hash)
	if err != nil || invRow == nil {
		t.Fatalf("load invite: err=%v inv=%v", err, invRow)
	}

	user, err := svc.Signup(context.Background(), signupInput("invitee", "bob", invRow))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy == nil || user.InvitedBy.ID != "inviter@srv" {
		t.Fatalf("invitedBy = %+v", user.InvitedBy)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM user_following`).Scan(&n)
	if n != 0 {
		t.Fatalf("follow edges = %d, want 0", n)
	}
}

func TestSignup_OpenInvalidToken(t *testing.T) {
	db := openSignupTestDB(t)
	ctx := context.Background()
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")

	// The invite's created_by FK requires a real user row — a signup
	// carrying an already-claimed invite reaches Signup only via
	// GetPendingInvite, which always resolves a real DB row. Reproduce
	// that shape here (real creator, already-claimed invite) rather than
	// a creator that was never inserted, which trips the FK constraint
	// before Signup's own pending-status check ever runs.
	if _, err := svc.Signup(ctx, signupInput("inviter", "carol", nil)); err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Now().UTC()
	claimedBy := "someone-else"
	_, err := svc.Signup(ctx, signupInput("u1", "alice", &invites.Invite{
		ID:          "abcdefghijkl",
		CreatedBy:   "inviter",
		GrantedRole: roles.RoleUser,
		ClaimedAt:   &claimedAt,
		ClaimedBy:   &claimedBy,
	}))
	if !errors.Is(err, invites.ErrInvalidInvite) {
		t.Fatalf("err = %v", err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 'u1'`).Scan(&n)
	if n != 0 {
		t.Fatal("user should not be created")
	}
}

func TestSignup_RootMintRole(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")

	user, err := svc.Signup(context.Background(), signupInput(roles.RootUserID, "root", nil))
	if err != nil {
		t.Fatal(err)
	}
	wantID := roles.RootUserID + "@srv"
	if user.ID != wantID {
		t.Fatalf("id = %q want %q", user.ID, wantID)
	}
	if role := queryUserRole(t, db, wantID); role != roles.RoleRoot {
		t.Fatalf("role = %q want %q", role, roles.RoleRoot)
	}
	got, err := svc.GetUserRole(context.Background(), wantID)
	if err != nil || got != roles.RoleRoot {
		t.Fatalf("GetUserRole = %q err=%v want %q", got, err, roles.RoleRoot)
	}
}

func TestSignup_AdminInviteGrantsAdminRole(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.setServerIDForTest("srv")
	ctx := context.Background()

	if _, err := svc.Signup(context.Background(), signupInput("inviter", "alice", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET role = $1 WHERE id = $2`, roles.RoleAdmin, "inviter"); err != nil {
		t.Fatal(err)
	}

	raw, err := invites.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash := invites.HashSecret(raw)
	id, err := invites.NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	store := &invites.Store{DB: db, ServerID: "srv"}
	if err := store.Insert(ctx, id, "inviter@srv", hash, time.Now().UTC(), roles.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	invRow, err := store.GetByTokenHash(ctx, hash)
	if err != nil || invRow == nil {
		t.Fatalf("load invite: err=%v inv=%v", err, invRow)
	}

	user, err := svc.Signup(context.Background(), signupInput("invitee", "bob", invRow))
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "invitee@srv" {
		t.Fatalf("id = %q", user.ID)
	}
	if role := queryUserRole(t, db, "invitee@srv"); role != roles.RoleAdmin {
		t.Fatalf("invitee role = %q want %q", role, roles.RoleAdmin)
	}
}
