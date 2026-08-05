package realtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectConnection(t *testing.T) {
	rr := httptest.NewRecorder()
	rejectConnection(rr, "Finish recovery import first.")

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body errorMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "Finish recovery import first." {
		t.Fatalf("error = %q", body.Error)
	}
}

func TestDeviceMismatch(t *testing.T) {
	t.Run("nil check allows", func(t *testing.T) {
		rs := &RealtimeService{}
		if rs.deviceMismatch("u1", "dev") {
			t.Fatal("expected allow")
		}
	})

	t.Run("check failure rejects", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetDeviceCheck(func(userID, deviceID string) error {
			return errors.New("mismatch")
		})
		if !rs.deviceMismatch("u1", "dev") {
			t.Fatal("expected mismatch")
		}
	})

	t.Run("check success allows", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetDeviceCheck(func(userID, deviceID string) error {
			return nil
		})
		if rs.deviceMismatch("u1", "dev") {
			t.Fatal("expected allow")
		}
	})
}

func TestOngoingImport(t *testing.T) {
	t.Run("nil check allows", func(t *testing.T) {
		rs := &RealtimeService{}
		ongoing, err := rs.ongoingImport("u1")
		if err != nil {
			t.Fatal(err)
		}
		if ongoing {
			t.Fatal("expected allow")
		}
	})

	t.Run("ongoing", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetOngoingCheck(func(userID string) (bool, error) {
			return true, nil
		})
		ongoing, err := rs.ongoingImport("u1")
		if err != nil {
			t.Fatal(err)
		}
		if !ongoing {
			t.Fatal("expected ongoing")
		}
	})

	t.Run("not ongoing", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetOngoingCheck(func(userID string) (bool, error) {
			return false, nil
		})
		ongoing, err := rs.ongoingImport("u1")
		if err != nil {
			t.Fatal(err)
		}
		if ongoing {
			t.Fatal("expected allow")
		}
	})
}
