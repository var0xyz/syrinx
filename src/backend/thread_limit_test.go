//go:build !ops

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func ensureThreadLimitSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE reed_replies (
		thread_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) PRIMARY KEY,
		parent_reed_id VARCHAR(255) NOT NULL,
		timestamp TIMESTAMP NOT NULL
	)`)
	return err
}

func TestSelfReplyChainLength(t *testing.T) {
	db := newTestDatabase(t, ensureThreadLimitSchema)
	ds := &DataService{db: db, serverID: "testserver"}
	ctx := context.Background()

	author := "7@testserver"
	other := "12@testserver"
	reed := func(user string, n int) string { return fmt.Sprintf("%s/r%d", user, n) }
	reply := func(child, parent string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO reed_replies VALUES ($1, $2, $3, $4)`,
			reed(other, 0), child, parent, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	// other's root, then author's self-chain r1..r5 hanging off it.
	reply(reed(author, 1), reed(other, 0))
	for i := 2; i <= 5; i++ {
		reply(reed(author, i), reed(author, i-1))
	}

	tests := []struct {
		parent string
		limit  int
		want   int
	}{
		{reed(author, 1), MaxThreadReeds, 1},
		{reed(author, 5), MaxThreadReeds, 5},
		{reed(author, 5), 3, 3},
		{reed(other, 0), MaxThreadReeds, 1},
	}
	for _, tc := range tests {
		got, err := ds.SelfReplyChainLength(ctx, tc.parent, author, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("SelfReplyChainLength(%s, limit %d) = %d, want %d", tc.parent, tc.limit, got, tc.want)
		}
	}
}
