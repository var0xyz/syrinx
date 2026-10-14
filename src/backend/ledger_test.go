//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// ledger returns every entry in chain order, starting at the head.
func (f *adminFixture) ledger(t *testing.T) []*ledgerEntry {
	t.Helper()
	ctx := context.Background()
	head, err := f.ds.GetLedgerHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if head == nil {
		return nil
	}
	tail, err := f.ds.GetLedgerTail(ctx, head.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return append([]*ledgerEntry{head}, tail...)
}

// verifyChain checks every entry's server signature over its payload,
// built with the previous entry's signature.
func (f *adminFixture) verifyChain(t *testing.T, entries []*ledgerEntry) {
	t.Helper()
	prevSig := ""
	var prevID uint64
	for _, e := range entries {
		if e.PrevID != prevID {
			t.Fatalf("entry %d prevID=%d, want %d", e.ID, e.PrevID, prevID)
		}
		payload := buildLedgerEntryPayload(e.ID, e.PrevID, prevSig, e.UserID, e.Message, e.Content,
			e.Targets, f.srv, f.h.signingKey.Fingerprint, e.ServerSignature.SignedAt)
		if err := f.h.services.crypto.verifySignature(string(payload), e.ServerSignature.Armor, f.h.signingKey.Armor); err != nil {
			t.Fatalf("entry %d signature: %v", e.ID, err)
		}
		prevSig, prevID = e.ServerSignature.Armor, e.ID
	}
}

func TestLedger_RequestWritesOpenedAndClosedEntries(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "a1", adminActionKick, "Scam links", "", "u1", "u2")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}

	entries := f.ledger(t)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	opened, closed := entries[0], entries[1]
	if opened.UserID != f.ids["a1"] || opened.Message != "["+msg.Id+"] Kick. Reason: Scam links" ||
		!slices.Equal(opened.Targets, msg.Targets) {
		t.Errorf("opened entry = %+v", opened)
	}
	if closed.UserID != f.ids["a2"] || closed.Message != "["+msg.Id+"] Approved" || len(closed.Targets) != 0 {
		t.Errorf("closed entry = %+v", closed)
	}
	for _, e := range entries {
		for _, name := range []string{"u1", "u2", "a1", "a2"} {
			if strings.Contains(e.Message, name) && !strings.Contains(msg.Id, name) {
				t.Errorf("entry %d message %q carries a username", e.ID, e.Message)
			}
		}
	}
	f.verifyChain(t, entries)
}

func TestLedger_PermissionsAndBroadcastCarryContent(t *testing.T) {
	f := newAdminFixture(t)
	promote := f.request(t, "root", adminActionPromote, "Helps a lot", formatPermissionSet([]string{permUserKick}), "u1")
	f.openAs(t, "root", promote)
	broadcast := f.request(t, "root", adminActionBroadcast, "", "Maintenance tonight")
	f.openAs(t, "root", broadcast)

	entries := f.ledger(t)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Content != permUserKick || entries[0].Message != "["+promote.Id+"] Promotion. Reason: Helps a lot" {
		t.Errorf("promote entry = %+v", entries[0])
	}
	if entries[1].Content != "Maintenance tonight" || entries[1].Message != "["+broadcast.Id+"] Broadcast" {
		t.Errorf("broadcast entry = %+v", entries[1])
	}
	f.verifyChain(t, entries)
}

func TestLedger_PrivateMessageWritesNoEntry(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "root", adminActionUnicast, "", "Can we talk?", "u1")
	f.openAs(t, "root", msg)
	if rr := f.decide(t, "root", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	if entries := f.ledger(t); len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}
}

func TestLedger_MootEntryHasNoUser(t *testing.T) {
	f := newAdminFixture(t)
	msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", msg)
	if _, err := f.db.Exec(`
		WITH us AS (INSERT INTO user_signatures (public_key_id, signature) VALUES ($2, 's') RETURNING id),
		     ss AS (INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id)
		INSERT INTO account_removals (user_id, public_key_id, user_signature_id, server_signature_id)
		SELECT $1, $2, us.id, ss.id FROM us, ss
	`, f.ids["u1"], f.keyID("u1")); err != nil {
		t.Fatal(err)
	}
	f.h.dropFromAdminRequests(context.Background(), f.ids["u1"])

	entries := f.ledger(t)
	if len(entries) != 2 || entries[1].UserID != "" || entries[1].Message != "["+msg.Id+"] Closed. Reason: every target left" {
		t.Fatalf("entries = %+v, want opened then a server-closed moot entry", entries)
	}
	f.verifyChain(t, entries)
}

func TestLedger_FailedExecutorWritesNoEntry(t *testing.T) {
	f := newAdminFixture(t)
	f.h.adminExecutorOverride[adminActionKick] = adminExecutor{execute: func(context.Context, *sql.Tx, *adminRequest) error {
		return errors.New("boom")
	}}
	msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusInternalServerError {
		t.Fatalf("approve with a failing executor: %d, want 500", rr.Code)
	}
	if f.state(t, msg.Id) != adminStatePending {
		t.Fatal("request closed although the executor failed")
	}
	reject := f.decide(t, "a2", msg.Id, adminDecisionReject)
	if reject.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", reject.Code, reject.Body.String())
	}
	entries := f.ledger(t)
	if len(entries) != 2 || entries[1].Message != "["+msg.Id+"] Rejected" {
		t.Fatalf("entries = %+v, want the opened and rejected entries only", entries)
	}
	f.verifyChain(t, entries)
}

func TestLedger_SingleChain(t *testing.T) {
	f := newAdminFixture(t)
	f.openAs(t, "a1", f.request(t, "a1", adminActionKick, "Spam", "", "u1"))
	f.openAs(t, "a1", f.request(t, "a1", adminActionKick, "Spam", "", "u2"))
	entries := f.ledger(t)

	var sigID int64
	if err := f.db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('k', 's', NOW()) RETURNING id`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO ledger_entries (message, signed_at, server_signature_id) VALUES ('x', NOW(), $1)`, sigID); err == nil {
		t.Error("a second head was stored")
	}
	if _, err := f.db.Exec(`INSERT INTO ledger_entries (prev_id, message, signed_at, server_signature_id) VALUES ($1, 'x', NOW(), $2)`,
		entries[0].ID, sigID); err == nil {
		t.Error("a fork was stored")
	}
}

func TestGetLedger_HeadTailAndAuth(t *testing.T) {
	f := newAdminFixture(t)
	var opened []string
	for range 3 {
		msg := f.request(t, "a1", adminActionKick, "Spam", "", "u1")
		f.openAs(t, "a1", msg)
		opened = append(opened, msg.Id)
	}

	rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger", "u2", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ledger: %d %s", rr.Code, rr.Body.String())
	}
	var page pb.LedgerPage
	if err := proto.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Head == nil || page.Head.PrevId != nil || len(page.Tail) != 2 ||
		page.Tail[0].GetPrevId() != page.Head.Id || page.Tail[1].GetPrevId() != page.Tail[0].Id {
		t.Fatalf("page = %+v, want a head and a chained tail of two", &page)
	}
	if !strings.Contains(page.Head.Message, opened[0]) {
		t.Errorf("head = %q, want the first request", page.Head.Message)
	}

	after := strconv.FormatUint(page.Tail[0].Id, 10)
	rr = f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger?after="+after, "u2", nil, nil)
	page = pb.LedgerPage{}
	if err := proto.Unmarshal(rr.Body.Bytes(), &page); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("after: %d %v", rr.Code, err)
	}
	if page.Head != nil || len(page.Tail) != 1 || !strings.Contains(page.Tail[0].Message, opened[2]) {
		t.Fatalf("after page = %+v, want only the last entry", &page)
	}

	last := strconv.FormatUint(page.Tail[0].Id, 10)
	rr = f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger?after="+last, "u2", nil, nil)
	page = pb.LedgerPage{}
	if err := proto.Unmarshal(rr.Body.Bytes(), &page); err != nil || len(page.Tail) != 0 {
		t.Fatalf("after the last entry: tail=%d err=%v, want empty", len(page.Tail), err)
	}

	if rr := f.serve(t, f.h.GetLedger, http.MethodGet, "/api/ledger?after=999999", "u2", nil, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown after: %d, want 404", rr.Code)
	}
	rr = f.serve(t, f.h.GetLedgerEntryHandler, http.MethodGet, "/api/ledger/"+last, "u2", nil, map[string]string{"id": last})
	if rr.Code != http.StatusOK {
		t.Errorf("GET /ledger/{id}: %d", rr.Code)
	}
	unsigned := httptest.NewRecorder()
	f.h.signatureAuthMiddleware("/api")(http.HandlerFunc(f.h.GetLedger)).ServeHTTP(unsigned, protoRequest(http.MethodGet, "/api/ledger", nil))
	if unsigned.Code == http.StatusOK {
		t.Errorf("unsigned read: %d, want refused", unsigned.Code)
	}
}
