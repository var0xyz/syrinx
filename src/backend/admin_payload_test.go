//go:build !ops

package main

import (
	"testing"
	"time"
)

// Golden bytes. The SPA harness asserts the same strings; they must stay
// byte-identical or no admin request verifies.
func TestAdminRequestPayloadCanonicalShape(t *testing.T) {
	createdAt := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	got := buildAdminRequestPayload("5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50", "kick", "a1@home1234",
		"a1@home1234/4a1e", "Spam", []string{"u1@home1234", "u2@home1234"}, "", createdAt)
	want := `{"action":"kick","createdAt":"2026-10-12T09:30:00Z","keyID":"a1@home1234/4a1e",` +
		`"reason":"Spam","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requesterID":"a1@home1234",` +
		`"targets":["u1@home1234","u2@home1234"],"type":"admin-request"}`
	if string(got) != want {
		t.Errorf("admin request payload mismatch:\n got=%q\nwant=%q", got, want)
	}

	got = buildAdminRequestPayload("5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50", "broadcast", "a1@home1234",
		"a1@home1234/4a1e", "", nil, "Maintenance tonight", createdAt)
	want = `{"action":"broadcast","body":"Maintenance tonight","createdAt":"2026-10-12T09:30:00Z",` +
		`"keyID":"a1@home1234/4a1e","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",` +
		`"requesterID":"a1@home1234","type":"admin-request"}`
	if string(got) != want {
		t.Errorf("broadcast request payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAdminDecisionPayloadCanonicalShape(t *testing.T) {
	got := buildAdminDecisionPayload("5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50", "approve", "a2@home1234",
		"a2@home1234/9b2f", "REQSIG")
	want := `{"deciderID":"a2@home1234","decision":"approve","keyID":"a2@home1234/9b2f",` +
		`"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG","type":"admin-decision"}`
	if string(got) != want {
		t.Errorf("admin decision payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAdminRequestServerPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	got := buildAdminRequestServerPayload("5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50", "executed", "REQSIG",
		"DECSIG", "home1234", "SERVERKEY01", signedAt)
	want := `{"decisionSignature":"DECSIG","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",` +
		`"requestSignature":"REQSIG","serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-12T10:00:00Z","state":"executed","type":"admin-request-server"}`
	if string(got) != want {
		t.Errorf("admin server payload mismatch:\n got=%q\nwant=%q", got, want)
	}

	got = buildAdminRequestServerPayload("5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50", "moot", "REQSIG",
		"", "home1234", "SERVERKEY01", signedAt)
	want = `{"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG",` +
		`"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-12T10:00:00Z","state":"moot","type":"admin-request-server"}`
	if string(got) != want {
		t.Errorf("moot server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestLedgerEntryPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	got := buildLedgerEntryPayload(1, 0, "", "a1@home1234", "Admin invite inv1 created",
		"admin.create,user.kick", nil, "home1234", "SERVERKEY01", signedAt)
	want := `{"content":"admin.create,user.kick","id":1,"message":"Admin invite inv1 created",` +
		`"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",` +
		`"type":"ledger-entry","userID":"a1@home1234"}`
	if string(got) != want {
		t.Errorf("first ledger entry payload mismatch:\n got=%q\nwant=%q", got, want)
	}

	got = buildLedgerEntryPayload(7, 5, "PREVSIG", "", "[r1] Closed. Reason: every target left",
		"", []string{"u1@home1234", "u2@home1234"}, "home1234", "SERVERKEY01", signedAt)
	want = `{"id":7,"message":"[r1] Closed. Reason: every target left","prevID":5,"prevSignature":"PREVSIG",` +
		`"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",` +
		`"targets":["u1@home1234","u2@home1234"],"type":"ledger-entry"}`
	if string(got) != want {
		t.Errorf("chained ledger entry payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAccountKickServerPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	got := buildAccountKickServerPayload("home1234", "u1@home1234", "5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",
		"REQSIG", "DECSIG", "SERVERKEY01", signedAt)
	want := `{"decisionSignature":"DECSIG","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",` +
		`"requestSignature":"REQSIG","serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",` +
		`"signedAt":"2026-10-12T12:00:00Z","type":"account-kick","userID":"u1@home1234"}`
	if string(got) != want {
		t.Errorf("kick server payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAdminRevocationServerPayloadCanonicalShape(t *testing.T) {
	signedAt := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	got := buildAdminRevocationServerPayload("u1@home1234", "u1@home1234/OLDKEY", "5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",
		"REQSIG", "DECSIG", "home1234", "SERVERKEY01", signedAt)
	want := `{"decisionSignature":"DECSIG","keyID":"u1@home1234/OLDKEY","reason":"lost",` +
		`"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG","serverID":"home1234",` +
		`"serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T13:00:00Z","type":"admin-revocation",` +
		`"userID":"u1@home1234"}`
	if string(got) != want {
		t.Errorf("admin revocation payload mismatch:\n got=%q\nwant=%q", got, want)
	}
}
