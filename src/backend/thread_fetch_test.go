//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

// threadEventFor returns the requester's thread event for threadID, or "".
func threadEventFor(t *testing.T, ds *DataService, requester, threadID string) string {
	t.Helper()
	var eventID string
	err := ds.db.QueryRow(`
		SELECT pe.event_id FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.requester_user_id = $1 AND pre.reed_id = $2 AND pe.event_name = $3`,
		requester, threadID, string(requestThreadEvent)).Scan(&eventID)
	if err != nil {
		return ""
	}
	return eventID
}

func allocatedTo(t *testing.T, ds *DataService, userID string) []string {
	t.Helper()
	rows, err := ds.db.Query(`SELECT reed_id FROM reed_allocations WHERE holder_user_id = $1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// relayed marks eventID as answered by holder and delivered.
func relayed(t *testing.T, ds *DataService, eventID, holder string) {
	t.Helper()
	ctx := context.Background()
	if _, err := ds.MarkEventDispatched(ctx, eventID, holder); err != nil {
		t.Fatal(err)
	}
	if err := ds.MarkEventRelayed(ctx, eventID); err != nil {
		t.Fatal(err)
	}
}

func TestThreadFetch_AckAllocatesEveryPart(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	ds := f.h.services.db
	rs := newRealtimeService(ds, newCryptoService(), "")
	ids := f.seedThread(t, [][]string{nil, nil, nil}, [][]string{nil, nil, nil})
	signedUpUser(t, f.h, "u2", "bob")
	bob := string(canonicalID(ds.GetServerID(), "u2"))
	for _, u := range []string{f.author, bob} {
		if err := ds.MarkUserOnline(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	rs.requestThread(&realtimeClient{userID: bob}, generateRealtimeEventID(bob), ids[0])
	eventID := threadEventFor(t, ds, bob, ids[0])
	if eventID == "" {
		t.Fatal("no thread event registered")
	}
	relayed(t, ds, eventID, f.author)
	rs.handleDataAck(&realtimeClient{userID: bob}, eventID)

	want := append([]string{}, ids...)
	sort.Strings(want)
	if got := allocatedTo(t, ds, bob); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("allocated = %v, want every part %v", got, want)
	}
}

func TestThreadFetch_UnknownThread(t *testing.T) {
	f := newThreadFixture(t)
	ctx := context.Background()
	ds := f.h.services.db
	rs := newRealtimeService(ds, newCryptoService(), "")
	plain := f.ids(t, 1)[0]
	if _, err := ds.CreateReed(ctx, createReedParams{
		ReedID: plain, UserID: f.author, UserKeyID: "k", UserSignature: "s",
		ServerFingerprint: "f", ServerSignature: "s",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ds.MarkUserOnline(ctx, f.author); err != nil {
		t.Fatal(err)
	}

	rs.requestThread(&realtimeClient{userID: f.author}, generateRealtimeEventID(f.author), plain)
	if threadEventFor(t, ds, f.author, plain) != "" {
		t.Fatal("a plain reed was requested as a thread")
	}
	if result, _, _ := rs.HandleForeignRequestThread(ctx, plain, "peer", "x@peer", "x@peer/k"); result != realtimeForeignRequestReedNotFound {
		t.Fatalf("peer request for a plain reed: %v, want not found", result)
	}
}

// The home server sends the record with a relayed thread.
func TestThreadFetch_RelayCarriesRecord(t *testing.T) {
	f := newThreadFixture(t)
	rs := newRealtimeService(f.h.services.db, newCryptoService(), "")
	ids := f.seedThread(t, [][]string{nil, nil}, [][]string{nil, nil})

	var out relayedThread
	if err := json.Unmarshal(rs.relayedThreadData(ids[0], "ct"), &out); err != nil {
		t.Fatal(err)
	}
	if out.Ciphertext != "ct" || out.Record == nil || fmt.Sprint(out.Record.ReedIDs) != fmt.Sprint(ids) {
		t.Fatalf("relayed thread = %+v, want ciphertext and record of %v", out, ids)
	}
}

// foreignThread returns a three-part thread by a peer's user.
func foreignThread(t *testing.T) []string {
	t.Helper()
	author := string(canonicalID(teardownPeerID, "bob"))
	ids := make([]string, 3)
	for i := range ids {
		ids[i] = author + "/" + newTestReedID(t)
	}
	return ids
}

func TestThreadFetch_ForeignAckUsesVerifiedRecord(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verified bool
	}{{"verified record", true}, {"unverifiable record", false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, rs, _ := newTeardownTestService(t)
			ctx := context.Background()
			insertTeardownIdentity(t, db, string(canonicalID(teardownPeerID, "bob")), teardownPeerID)
			ids := foreignThread(t)
			rs.SetForeignRequestThreadHook(func(context.Context, string, string, string) (realtimeForeignRequestResult, string, error) {
				return realtimeForeignRequestOK, "peer-ev-1", nil
			})
			reader := string(canonicalID(teardownHomeID, "reader"))
			insertTeardownIdentity(t, db, reader, teardownHomeID)
			if _, err := db.Exec(`INSERT INTO users (id) VALUES ($1)`, reader); err != nil {
				t.Fatal(err)
			}
			if err := rs.db.MarkUserOnline(ctx, reader); err != nil {
				t.Fatal(err)
			}

			rs.requestThread(&realtimeClient{userID: reader}, generateRealtimeEventID(reader), ids[0])
			eventID := threadEventFor(t, rs.db, reader, ids[0])
			if eventID == "" {
				t.Fatal("no thread event registered")
			}
			var verified []string
			if tc.verified {
				verified = ids
			}
			data, _ := json.Marshal(relayedThread{Ciphertext: "ct"})
			if found, err := rs.HandleForeignRelayResponse(ctx, "peer-ev-1", teardownPeerID, data, verified); err != nil || !found {
				t.Fatalf("HandleForeignRelayResponse: %v, %v", found, err)
			}
			if err := rs.db.MarkEventRelayed(ctx, eventID); err != nil {
				t.Fatal(err)
			}
			rs.handleDataAck(&realtimeClient{userID: reader}, eventID)

			got := allocatedTo(t, rs.db, reader)
			if !tc.verified {
				if len(got) != 0 {
					t.Fatalf("allocated %v from an unverified record", got)
				}
				return
			}
			want := append([]string{}, ids...)
			sort.Strings(want)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("allocated = %v, want every part %v", got, want)
			}
		})
	}
}

// Shape checks fail before any key is fetched.
func TestVerifyPeerThreadRecord_RejectsShape(t *testing.T) {
	h := &Handlers{}
	ids := foreignThread(t)
	good := threadRecordWire{
		Type: identityTypeThread, ServerID: teardownPeerID,
		UserID: string(canonicalID(teardownPeerID, "bob")), ThreadID: ids[0], ReedIDs: ids,
	}
	cases := map[string]func(*threadRecordWire){
		"wrong type":       func(r *threadRecordWire) { r.Type = "reed" },
		"another server":   func(r *threadRecordWire) { r.ServerID = "elsewhere" },
		"head isn't first": func(r *threadRecordWire) { r.ThreadID = ids[1] },
		"one part":         func(r *threadRecordWire) { r.ReedIDs = ids[:1] },
		"another's reed": func(r *threadRecordWire) {
			r.ReedIDs = append([]string{}, ids...)
			r.ReedIDs[2] = "eve@" + teardownPeerID + "/" + newTestReedID(t)
		},
		"key of another user": func(r *threadRecordWire) { r.UserSignature.ID = "eve@" + teardownPeerID + "/k" },
	}
	for name, mutate := range cases {
		rec := good
		mutate(&rec)
		if _, err := h.verifyPeerThreadRecord(context.Background(), teardownPeerID, rec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
