//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrinx/identity"
)

// newBareRelayTestHandlers builds a *Handlers with just enough wired up to
// exercise RelayRequestFromPeer/DeliverRelayResponseFromPeer/
// CancelRelayRequestFromPeer's auth-rejection and loop-prevention checks —
// both run entirely before any DB call (peerServerIDKey presence, then a
// pure identity.ParseIdentityID comparison against GetServerID()), so no
// live database is needed for this slice of coverage. h.realtimeRelay is
// left nil; these tests never get far enough to reach it.
func newBareRelayTestHandlers(serverID string) *Handlers {
	return &Handlers{
		services: &Services{
			db:  &DataService{serverID: serverID},
			log: NewLoggingService(),
		},
	}
}

func withPeer(r *http.Request, peerServerID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), peerServerIDKey, peerServerID))
}

func TestRelayRequestFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"01a026d4","author_id":"alice@home1234","requester_user_id":"bob@peer5678","peer_request_id":"r1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/request", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no peerServerIDKey in context)", rr.Code, http.StatusUnauthorized)
	}
}

func TestRelayRequestFromPeer_RejectsForeignAuthorID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	// author_id's embedded serverID ("thirdparty") is neither this
	// server's own id nor the calling peer's -- this is exactly the
	// chained/multi-hop relay the loop-prevention guard must reject.
	body := `{"reed_id":"01a026d4","author_id":"alice@thirdparty","requester_user_id":"bob@peer5678","peer_request_id":"r1"}`
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/request", strings.NewReader(body)), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (author_id not local to this server)", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayRequestFromPeer_RejectsMissingFields(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"","author_id":"","requester_user_id":"","peer_request_id":""}`
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/request", strings.NewReader(body)), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayRequestFromPeer_AcceptsAuthorLocalToThisServer(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"01a026d4","author_id":"alice@home1234","requester_user_id":"bob@peer5678","peer_request_id":"bob@peer5678/r1"}`
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/request", strings.NewReader(body)), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	// realtimeRelay is nil past the loop-prevention check, so this can't
	// reach 200 -- but it must NOT be rejected as 400/401 (that would mean
	// a legitimate same-author request was wrongly treated as foreign).
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusBadRequest {
		t.Fatalf("status = %d, want the request to pass auth + loop-prevention (author_id is local)", rr.Code)
	}
}

func TestDeliverRelayResponseFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":"evt1","data":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/deliver", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.DeliverRelayResponseFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeliverRelayResponseFromPeer_RejectsMissingPeerEventID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":"","data":{}}`
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/deliver", strings.NewReader(body)), "peer5678")
	rr := httptest.NewRecorder()

	h.DeliverRelayResponseFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCancelRelayRequestFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":"evt1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/federation/relay/cancel", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CancelRelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestCancelRelayRequestFromPeer_RejectsMissingPeerEventID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":""}`
	req := withPeer(httptest.NewRequest(http.MethodPost, "/api/federation/relay/cancel", strings.NewReader(body)), "peer5678")
	rr := httptest.NewRecorder()

	h.CancelRelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestRelayRequestCanonicalReedIDReconstruction confirms the exact
// reconstruction RelayRequestFromPeer performs from trusted parts
// (this server's own GetServerID() + the parsed author's bare userID +
// the peer-supplied bare reed_id) matches identity.AppendEntity's shape,
// since a mismatch here would silently misroute every registered request.
func TestRelayRequestCanonicalReedIDReconstruction(t *testing.T) {
	authorUserID, embeddedServerID, ok := identity.ParseIdentityID(identity.IdentityID("alice@home1234"))
	if !ok || embeddedServerID != "home1234" {
		t.Fatalf("ParseIdentityID unexpected result: userID=%q serverID=%q ok=%v", authorUserID, embeddedServerID, ok)
	}
	got := string(identity.AppendEntity(identity.CanonicalID("home1234", authorUserID), "01a026d4"))
	want := "alice@home1234/01a026d4"
	if got != want {
		t.Fatalf("canonical reedID = %q, want %q", got, want)
	}
}
