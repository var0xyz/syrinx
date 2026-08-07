//go:build !ops

package main

import (
	"context"
	"testing"

	"syrinx/invites"
	"syrinx/roles"
)

func TestRequireRootUser_RecoveryModeSkips(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	cfg := AppConfig{RecoveryMode: true}
	if err := requireRootUser(cfg, svc); err != nil {
		t.Fatalf("recovery mode should skip: %v", err)
	}
}

func TestRequireRootUser_MissingRootFails(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	cfg := AppConfig{RecoveryMode: false}
	if err := requireRootUser(cfg, svc); err == nil {
		t.Fatal("expected error when root row missing")
	}
}

func TestRequireRootUser_PresentRootOK(t *testing.T) {
	db := openSignupTestDB(t)
	svc := NewDataService(db, "test")
	svc.serverID = "srv"
	if _, err := svc.Signup(context.Background(), signupInput(roles.RootUserID, "root", "", "", "", invites.ModeOpen)); err != nil {
		t.Fatal(err)
	}
	cfg := AppConfig{RecoveryMode: false}
	if err := requireRootUser(cfg, svc); err != nil {
		t.Fatalf("expected ok when root exists: %v", err)
	}
}
