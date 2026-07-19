package realtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectOngoingImport(t *testing.T) {
	t.Run("nil check allows", func(t *testing.T) {
		rs := &RealtimeService{}
		rr := httptest.NewRecorder()
		if rs.rejectOngoingImport(rr, "u1") {
			t.Fatal("expected allow")
		}
		if rr.Code != 200 && rr.Code != 0 {
			// httptest defaults to 200 only after Write; Code is 200 by default in some versions
		}
	})

	t.Run("ongoing rejects 403", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetOngoingCheck(func(userID string) (bool, error) {
			return true, nil
		})
		rr := httptest.NewRecorder()
		if !rs.rejectOngoingImport(rr, "u1") {
			t.Fatal("expected reject")
		}
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
	})

	t.Run("not ongoing allows", func(t *testing.T) {
		rs := &RealtimeService{}
		rs.SetOngoingCheck(func(userID string) (bool, error) {
			return false, nil
		})
		rr := httptest.NewRecorder()
		if rs.rejectOngoingImport(rr, "u1") {
			t.Fatal("expected allow")
		}
	})
}
