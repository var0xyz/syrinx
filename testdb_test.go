//go:build !ops

package main

import (
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// testDBCounter disambiguates databases created within the same
// nanosecond — time.Now().UnixNano() alone is not guaranteed unique when
// tests create databases back-to-back on a coarse system clock.
var testDBCounter int64

func testEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func testDSN(dbName string) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		testEnvOr("DB_HOST", "localhost"),
		testEnvOr("DB_PORT", "5432"),
		testEnvOr("DB_USER", "syrinx"),
		testEnvOr("DB_PASSWORD", "syrinx"),
		dbName,
		testEnvOr("DB_SSLMODE", "disable"),
	)
}

// newTestDatabase creates a fresh, uniquely-named database for the
// duration of one test and drops it on cleanup. Each test file defines
// its own conflicting schema for shared table names (users, servers, …);
// previously they all raced against one static syrinx_test database,
// which corrupted whichever schema shape ran second. A private database
// per test removes that cross-file interference entirely.
//
// Returns a connection to the new database, already schema'd by
// ensureSchema. Skips the test (not fails) when Postgres is unreachable,
// matching prior behavior for environments without a local DB.
func newTestDatabase(t *testing.T, ensureSchema func(*sql.DB) error) *sql.DB {
	t.Helper()

	admin, err := sql.Open("postgres", testDSN(testEnvOr("DB_MAINTENANCE_NAME", "postgres")))
	if err != nil {
		t.Skipf("open admin db: %v", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Skipf("ping admin db: %v", err)
	}

	n := atomic.AddInt64(&testDBCounter, 1)
	dbName := fmt.Sprintf("syrinx_test_%d_%d", time.Now().UnixNano(), n)

	if _, err := admin.Exec(fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatalf("create test db %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		// New connection: the per-test db connection must be closed
		// before DROP DATABASE, and admin's pool may have gone stale.
		cleanupAdmin, err := sql.Open("postgres", testDSN(testEnvOr("DB_MAINTENANCE_NAME", "postgres")))
		if err != nil {
			return
		}
		defer cleanupAdmin.Close()
		_, _ = cleanupAdmin.Exec(fmt.Sprintf(
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`,
			dbName,
		))
		_, _ = cleanupAdmin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, dbName))
	})

	db, err := sql.Open("postgres", testDSN(dbName))
	if err != nil {
		t.Fatalf("open test db %s: %v", dbName, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping test db %s: %v", dbName, err)
	}
	t.Cleanup(func() { db.Close() })

	if err := ensureSchema(db); err != nil {
		t.Fatalf("schema for %s: %v", dbName, err)
	}
	return db
}
