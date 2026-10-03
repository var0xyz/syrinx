//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func ensureFollowsSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE identities (id VARCHAR(255) PRIMARY KEY, server_id VARCHAR(16))`,
		`CREATE TABLE users (id VARCHAR(255) PRIMARY KEY REFERENCES identities(id))`,
		`CREATE TABLE user_followers (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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

func TestRecordRemoteFollower_TargetMustBeLocalUser(t *testing.T) {
	db := newTestDatabase(t, ensureFollowsSchema)
	svc := NewDataService(db, "test")
	ctx := context.Background()
	for _, id := range []string{"alice@here", "bob@there", "carol@elsewhere"} {
		if _, err := db.Exec(`INSERT INTO identities (id) VALUES ($1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id) VALUES ('alice@here')`); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecordRemoteFollower(ctx, "alice@here", "bob@there"); err != nil {
		t.Fatalf("local target: %v", err)
	}
	for _, target := range []string{"nobody@here", "carol@elsewhere"} {
		if err := svc.RecordRemoteFollower(ctx, target, "bob@there"); !errors.Is(err, ErrFollowTargetNotFound) {
			t.Fatalf("target %s: err = %v, want ErrFollowTargetNotFound", target, err)
		}
	}
}
