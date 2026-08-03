//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"syrinx/invites"

	_ "github.com/lib/pq"
)

func openSignupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOr("DB_HOST", "localhost"),
		envOr("DB_PORT", "5432"),
		envOr("DB_USER", "syrinx"),
		envOr("DB_PASSWORD", "syrinx"),
		envOr("DB_NAME", "syrinx_test"),
		envOr("DB_SSLMODE", "disable"),
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("ping db: %v", err)
	}
	if err := ensureSignupInviteSchema(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureSignupInviteSchema(db *sql.DB) error {
	stmts := []string{
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
		`DROP TABLE IF EXISTS user_following CASCADE`,
		`DROP TABLE IF EXISTS user_followers CASCADE`,
		`DROP TABLE IF EXISTS invites CASCADE`,
		`DROP TABLE IF EXISTS user_keys CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			avatar_url VARCHAR(255),
			bio TEXT,
			user_fingerprint VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_signature_id INT NOT NULL REFERENCES user_signatures(id),
			server_signature_id INT NOT NULL REFERENCES server_signatures(id),
			invited_by VARCHAR(255) REFERENCES users(id)
		)`,
		`CREATE TABLE user_keys (
			fingerprint VARCHAR(255) PRIMARY KEY,
			owner VARCHAR(255) NOT NULL REFERENCES users(id),
			armor TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE invites (
			created_by VARCHAR(255) NOT NULL REFERENCES users(id),
			id VARCHAR(255) NOT NULL,
			token_hash BYTEA NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			claimed_at TIMESTAMPTZ,
			claimed_by VARCHAR(255) REFERENCES users(id),
			revoked_at TIMESTAMPTZ,
			PRIMARY KEY (created_by, id)
		)`,
		`CREATE TABLE user_following (
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			following_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, following_user_id)
		)`,
		`CREATE TABLE user_followers (
			user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			follower_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, follower_user_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func signupInput(userID, username, invitedBy, inviteID, secret string, mode invites.SignupMode) SignupInput {
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
		SignupMode:   mode,
		InviteID:     inviteID,
		InviteSecret: secret,
		InvitedBy:    invitedBy,
	}
}

func TestSignup_OpenNoInvite(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.serverID = "srv"

	user, err := svc.Signup(signupInput("u1", "alice", "", "", "", invites.ModeOpen))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy != nil {
		t.Fatalf("invitedBy = %+v, want nil", user.InvitedBy)
	}
}

func TestSignup_InviteRequired(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.serverID = "srv"

	_, err := svc.Signup(signupInput("u1", "alice", "", "", "", invites.ModeInvite))
	if !errors.Is(err, invites.ErrInviteRequired) {
		t.Fatalf("err = %v, want ErrInviteRequired", err)
	}
}

func TestSignup_ConsumeInvite(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.serverID = "srv"
	ctx := context.Background()

	if _, err := svc.Signup(signupInput("inviter", "alice", "", "", "", invites.ModeOpen)); err != nil {
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
	store := &invites.Store{DB: db}
	if err := store.Insert(ctx, id, "inviter", hash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	user, err := svc.Signup(signupInput("invitee", "bob", "inviter", id, raw, invites.ModeInvite))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy == nil || user.InvitedBy.ID != "inviter" || user.InvitedBy.Username != "alice" {
		t.Fatalf("invitedBy = %+v", user.InvitedBy)
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

	_, err = svc.Signup(signupInput("other", "carol", "inviter", id, raw, invites.ModeInvite))
	if !errors.Is(err, invites.ErrInvalidInvite) {
		t.Fatalf("reuse = %v, want ErrInvalidInvite", err)
	}
}

func TestSignup_OpenValidToken(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.serverID = "srv"
	ctx := context.Background()

	if _, err := svc.Signup(signupInput("inviter", "alice", "", "", "", invites.ModeOpen)); err != nil {
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
	if err := (&invites.Store{DB: db}).Insert(ctx, id, "inviter", hash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	user, err := svc.Signup(signupInput("invitee", "bob", "inviter", id, raw, invites.ModeOpen))
	if err != nil {
		t.Fatal(err)
	}
	if user.InvitedBy == nil || user.InvitedBy.ID != "inviter" {
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
	svc := NewDataService(db, "test")
	svc.serverID = "srv"
	_, err := svc.Signup(signupInput("u1", "alice", "nobody", "abcdefghijkl", "bad-secret", invites.ModeOpen))
	if !errors.Is(err, invites.ErrInvalidInvite) {
		t.Fatalf("err = %v", err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n != 0 {
		t.Fatal("user should not be created")
	}
}
