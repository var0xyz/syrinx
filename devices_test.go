//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"syrinx/identity"

	_ "github.com/lib/pq"
)

func openDevicesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOrDevices("DB_HOST", "localhost"),
		envOrDevices("DB_PORT", "5432"),
		envOrDevices("DB_USER", "syrinx"),
		envOrDevices("DB_PASSWORD", "syrinx"),
		envOrDevices("DB_NAME", "syrinx_test"),
		envOrDevices("DB_SSLMODE", "disable"),
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("ping db: %v", err)
	}
	if err := ensureDevicesSchema(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOrDevices(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureDevicesSchema(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS user_devices CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`CREATE TABLE users (
			id VARCHAR(255) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL
		)`,
		`CREATE TABLE user_devices (
			user_id    VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			device_id  TEXT NOT NULL,
			linked_at  TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ NULL,
			PRIMARY KEY (user_id, device_id, linked_at)
		)`,
		`CREATE UNIQUE INDEX user_devices_one_active_per_user
			ON user_devices (user_id) WHERE revoked_at IS NULL`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func insertDeviceTestUser(db *sql.DB, userID string) {
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES ($1, $2)`, userID, userID)
	if err != nil {
		panic(err)
	}
}

func deviceTestSvc(db *sql.DB) *DataService {
	return &DataService{db: db}
}

func TestBindDeviceTx_SameDeviceTwice(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	device := "550e8400-e29b-41d4-a716-446655440000"

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BindDeviceTx(context.Background(), tx, "u1", device, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BindDeviceTx(context.Background(), tx2, "u1", device, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	active, err := svc.GetActiveDeviceID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if active != device {
		t.Fatalf("active = %q", active)
	}
}

func TestBindDevice_BindRevokesPrevious(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d1 := "550e8400-e29b-41d4-a716-446655440000"
	d2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	if err := svc.BindDevice(context.Background(), "u1", d1, now); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindDevice(context.Background(), "u1", d2, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	active, err := svc.GetActiveDeviceID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if active != d2 {
		t.Fatalf("active = %q want %q", active, d2)
	}
}

func TestCheckActiveDevice(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Now().UTC()
	d1 := "550e8400-e29b-41d4-a716-446655440000"
	if err := svc.BindDevice(context.Background(), "u1", d1, now); err != nil {
		t.Fatal(err)
	}

	if err := svc.CheckActiveDevice(context.Background(), "u1", d1); err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := svc.CheckActiveDevice(context.Background(), "u1", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"); err != errDeviceMismatch {
		t.Fatalf("mismatch: %v", err)
	}
	if err := svc.CheckActiveDevice(context.Background(), "u1", ""); err != identity.ErrMissingDevice {
		t.Fatalf("missing: %v", err)
	}
}

func TestBindDevice_ConcurrentBind(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Now().UTC()
	d1 := "550e8400-e29b-41d4-a716-446655440000"
	d2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)

	go func() {
		defer wg.Done()
		err := svc.BindDevice(context.Background(), "u1", d1, now)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		err := svc.BindDevice(context.Background(), "u1", d2, now)
		errs <- err
	}()
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("bind error: %v", err)
		}
	}

	var activeCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM user_devices WHERE user_id = $1 AND revoked_at IS NULL
	`, "u1").Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active count = %d want 1", activeCount)
	}
}

func TestDeviceMiddleware(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Now().UTC()
	d1 := "550e8400-e29b-41d4-a716-446655440000"
	d2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	if err := svc.BindDevice(context.Background(), "u1", d1, now); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{services: &Services{db: svc}}
	mw := h.deviceMiddleware()
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	chain := mw(okHandler)

	withUID := func(r *http.Request, uid string) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), userIDKey, uid))
	}

	t.Run("unauthenticated passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
	})

	t.Run("matching device", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/users/me", nil), "u1")
		req.Header.Set("X-Syrinx-Device-Id", d1)
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/users/me", nil), "u1")
		req.Header.Set("X-Syrinx-Device-Id", d2)
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d", rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["error"] != "Device mismatch: this session is not bound to the active device." {
			t.Fatalf("error = %q", body["error"])
		}
	})

	t.Run("rebind exempt", func(t *testing.T) {
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/users/device", nil), "u1")
		req.Header.Set("X-Syrinx-Device-Id", d2)
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})
}

func TestBindDeviceHandler(t *testing.T) {
	db := openDevicesTestDB(t)
	insertDeviceTestUser(db, "u1")
	svc := deviceTestSvc(db)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	d1 := "550e8400-e29b-41d4-a716-446655440000"
	d2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	if err := svc.BindDevice(context.Background(), "u1", d1, now); err != nil {
		t.Fatal(err)
	}

	kicked := false
	h := &Handlers{
		services:   &Services{db: svc},
		kickUserWS: func(userID string) { kicked = true },
	}

	req := httptest.NewRequest(http.MethodPost, "/api/users/device", nil)
	req = req.WithContext(context.WithValue(req.Context(), userIDKey, "u1"))
	req.Header.Set("X-Syrinx-Device-Id", d2)
	rr := httptest.NewRecorder()
	h.BindDevice(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != d2 {
		t.Fatalf("body = %q want %q", got, d2)
	}
	if !kicked {
		t.Fatal("expected kick")
	}
	active, err := svc.GetActiveDeviceID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if active != d2 {
		t.Fatalf("active = %q", active)
	}
}
