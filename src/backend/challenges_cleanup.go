//go:build challengescleanup

// Standalone cron job: deletes recovery claim challenges older than
// challengeMaxAge. Separate from ripples-cleanup so each runs on its own
// schedule and one failing never stops the other.
//
// Build: go build -tags challengescleanup -o bin/challenges-cleanup .
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/tooxie/env"
)

type challengesCleanupConfig struct {
	DBHost     string `env:"name='DB_HOST'"`
	DBPort     string `env:"name='DB_PORT'"`
	DBUser     string `env:"name='DB_USER'"`
	DBPassword string `env:"name='DB_PASSWORD'"`
	DBName     string `env:"name='DB_NAME'"`
	DBSSLMode  string `env:"name='DB_SSLMODE'"`
}

func main() {
	var c challengesCleanupConfig
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
		`DELETE FROM recovery_challenges WHERE issued_at <= `+challengeCutoffSQL)
	if err != nil {
		fail(err)
	}
	n, _ := result.RowsAffected()
	fmt.Printf("%s challenges-cleanup: removed %d expired challenge(s)\n", time.Now().UTC().Format(time.RFC3339), n)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%s error: %v\n", time.Now().UTC().Format(time.RFC3339), err)
	os.Exit(1)
}
