//go:build ripplescleanup

// Standalone cron job: deletes expired ripple threads.
//
// Only the ripples bookkeeping table is targeted — ripple_responses rows
// cascade-delete through their FK to ripples(reed_author_id, reed_id),
// removing every thread's responses on that reed in the same statement.
//
// Runs as a separate process on a schedule (see jobs/ripples-cleanup.cron),
// not a goroutine inside the main server, since a goroutine's schedule
// would reset on every server restart. Reads DB_* the same way the main
// binary and the ops CLI do (process environment; source app.env first).
//
// Build: go build -tags ripplescleanup -o bin/ripples-cleanup .
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"
	"github.com/tooxie/env"
)

type ripplesCleanupConfig struct {
	DBHost     string `env:"name='DB_HOST'"`
	DBPort     string `env:"name='DB_PORT'"`
	DBUser     string `env:"name='DB_USER'"`
	DBPassword string `env:"name='DB_PASSWORD'"`
	DBName     string `env:"name='DB_NAME'"`
	DBSSLMode  string `env:"name='DB_SSLMODE'"`
}

func main() {
	var c ripplesCleanupConfig
	cfg := env.MustAssert(c)

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fail(err)
	}

	result, err := db.ExecContext(context.Background(),
		`DELETE FROM ripples WHERE expires_at <= NOW()`)
	if err != nil {
		fail(err)
	}
	n, _ := result.RowsAffected()
	fmt.Printf("ripples-cleanup: removed %d expired thread(s)\n", n)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
