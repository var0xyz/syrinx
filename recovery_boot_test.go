//go:build !ops

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrinx/recovery"
)

func TestErrNoIdentityFoundMentionsImportIdentity(t *testing.T) {
	if !strings.Contains(recovery.ErrNoIdentityFound.Error(), "import-identity") {
		t.Fatalf("error %q should mention import-identity", recovery.ErrNoIdentityFound)
	}
}

func TestGetServerInfoRecoveryMode(t *testing.T) {
	h := &Handlers{
		services: &Services{db: &DataService{}},
		cfg:      AppConfig{ServerName: "test.example", RecoveryMode: true},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/server/info", nil)
	h.GetServerInfo(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var info ServerInfo
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if !info.RecoveryMode {
		t.Fatal("expected recoveryMode true")
	}
	if info.Name != "test.example" {
		t.Fatalf("name = %q", info.Name)
	}
}
