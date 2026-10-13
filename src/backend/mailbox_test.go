//go:build !ops

package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSendMailboxMessage_EncryptsToActiveKey(t *testing.T) {
	db := newTestDatabase(t, InitDB)
	h := newInviteModeHandlers(t, db)
	kp := signedUpUser(t, h, "alice", "alice")
	userID := string(canonicalID(h.services.db.GetServerID(), "alice"))

	id, ciphertext, err := SendMailboxMessage(context.Background(), db, h.services.crypto, userID,
		"broadcast", "Maintenance tonight", "/ledger/1", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var stored string
	if err := db.QueryRow(`SELECT ciphertext FROM user_mailbox WHERE id = $1 AND user_id = $2`, id, userID).Scan(&stored); err != nil || stored != ciphertext {
		t.Fatalf("stored row = %q err=%v", stored, err)
	}
	plaintext, err := h.services.crypto.decrypt(ciphertext, kp.PrivateKey)
	if err != nil {
		t.Fatalf("decrypt with the active key: %v", err)
	}
	var payload MailboxPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != "broadcast" || payload.Message != "Maintenance tonight" || payload.Link != "/ledger/1" {
		t.Fatalf("payload = %+v", payload)
	}
}
