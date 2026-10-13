//go:build !ops

package main

import (
	"net/http"
	"sort"
	"testing"
	"time"
)

// mailboxRecipients waits for want rows of mail and returns who got one.
func (f *adminFixture) mailboxRecipients(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ids []string
		rows, err := f.db.Query(`SELECT user_id FROM user_mailbox`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) >= want || time.Now().After(deadline) {
			sort.Strings(ids)
			return ids
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBroadcast_ReachesEveryActiveMember(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionBroadcast, adminActionSuspend)
	f.suspend(t, "u2")
	f.grant(t, "a1", permBroadcastMessage)
	f.grant(t, "a2", permBroadcastMessage)

	msg := f.request(t, "a1", adminActionBroadcast, "", "Maintenance tonight")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve broadcast: %d %s", rr.Code, rr.Body.String())
	}

	var want []string
	for _, name := range []string{"root", "a1", "a2", "a3", "u1"} {
		want = append(want, f.ids[name])
	}
	sort.Strings(want)
	got := f.mailboxRecipients(t, len(want))
	if len(got) != len(want) {
		t.Fatalf("mail went to %v, want %v (none for the suspended member)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mail went to %v, want %v", got, want)
		}
	}
}

func TestPrivateMessage_ReachesOnlyItsRecipient(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionMessage)
	f.grant(t, "a1", permUserMessage)
	f.grant(t, "a2", permUserMessage)

	msg := f.request(t, "a1", adminActionMessage, "", "Can we talk about your replies?", "u1")
	f.openAs(t, "a1", msg)
	if rr := f.decide(t, "a2", msg.Id, adminDecisionApprove); rr.Code != http.StatusOK {
		t.Fatalf("approve message: %d %s", rr.Code, rr.Body.String())
	}
	if got := f.mailboxRecipients(t, 1); len(got) != 1 || got[0] != f.ids["u1"] {
		t.Fatalf("mail went to %v, want u1 only", got)
	}
	if entries := f.ledger(t); len(entries) != 0 {
		t.Fatalf("ledger = %+v, want no entry for a private message", entries)
	}
}
