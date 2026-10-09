//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// newBareRelayTestHandlers builds a *Handlers with just enough wired up to
// exercise RelayRequestFromPeer/DeliverRelayResponseFromPeer/
// CancelRelayRequestFromPeer's auth-rejection and loop-prevention checks —
// both run entirely before any DB call (peerServerIDKey presence, then a
// pure parseIdentityID comparison against GetServerID()), so no
// live database is needed for this slice of coverage. h.realtimeRelay is
// left nil; these tests never get far enough to reach it.
func newBareRelayTestHandlers(serverID string) *Handlers {
	return &Handlers{
		services: &Services{
			db:  &DataService{serverID: serverID},
			log: NewLoggingService(),
		},
		metrics: metrics.Noop{},
	}
}

func withPeer(r *http.Request, peerServerID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), peerServerIDKey, peerServerID))
}

func TestRelayRequestFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"01a026d4","author_id":"alice@home1234","requester_user_id":"bob@peer5678","peer_request_id":"r1"}`
	req := relayRequest("/api/federation/relay/request", &pb.RelayRequestPayload{}, body)
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
	req := withPeer(relayRequest("/api/federation/relay/request", &pb.RelayRequestPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (author_id not local to this server)", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayRequestFromPeer_RejectsMissingFields(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"","author_id":"","requester_user_id":"","peer_request_id":""}`
	req := withPeer(relayRequest("/api/federation/relay/request", &pb.RelayRequestPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayRequestFromPeer_AcceptsAuthorLocalToThisServer(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"reed_id":"01a026d4","author_id":"alice@home1234","requester_user_id":"bob@peer5678","requester_key_id":"bob@peer5678/k1","peer_request_id":"bob@peer5678/r1"}`
	req := withPeer(relayRequest("/api/federation/relay/request", &pb.RelayRequestPayload{}, body), "peer5678")
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
	body := `{"peer_event_id":"evt1"}`
	req := relayRequest("/api/federation/relay/deliver", &pb.RelayDeliverPayload{}, body)
	rr := httptest.NewRecorder()

	h.DeliverRelayResponseFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeliverRelayResponseFromPeer_RejectsMissingPeerEventID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":""}`
	req := withPeer(relayRequest("/api/federation/relay/deliver", &pb.RelayDeliverPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.DeliverRelayResponseFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCancelRelayRequestFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":"evt1"}`
	req := relayRequest("/api/federation/relay/cancel", &pb.RelayCancelPayload{}, body)
	rr := httptest.NewRecorder()

	h.CancelRelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestCancelRelayRequestFromPeer_RejectsMissingPeerEventID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"peer_event_id":""}`
	req := withPeer(relayRequest("/api/federation/relay/cancel", &pb.RelayCancelPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.CancelRelayRequestFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

// TestRelayRequestCanonicalReedIDReconstruction confirms the exact
// reconstruction RelayRequestFromPeer performs from trusted parts
// (this server's own GetServerID() + the parsed author's bare userID +
// the peer-supplied bare reed_id) matches appendEntity's shape,
// since a mismatch here would silently misroute every registered request.
func TestRelayRequestCanonicalReedIDReconstruction(t *testing.T) {
	authorUserID, embeddedServerID, ok := parseIdentityID(identityID("alice@home1234"))
	if !ok || embeddedServerID != "home1234" {
		t.Fatalf("ParseIdentityID unexpected result: userID=%q serverID=%q ok=%v", authorUserID, embeddedServerID, ok)
	}
	got := string(appendEntity(canonicalID("home1234", authorUserID), "01a026d4"))
	want := "alice@home1234/01a026d4"
	if got != want {
		t.Fatalf("canonical reedID = %q, want %q", got, want)
	}
}

func TestRelayProfilePageFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"author_id":"alice@home1234","requester_user_id":"bob@peer5678","page":1}`
	req := relayRequest("/api/federation/relay/profile-page", &pb.RelayProfilePagePayload{}, body)
	rr := httptest.NewRecorder()

	h.RelayProfilePageFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no peerServerIDKey in context)", rr.Code, http.StatusUnauthorized)
	}
}

func TestRelayProfilePageFromPeer_RejectsMalformedBody(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	req := withPeer(malformedRelayRequest("/api/federation/relay/profile-page"), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayProfilePageFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (malformed body)", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayProfilePageFromPeer_RejectsMissingFields(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"author_id":"","requester_user_id":"","page":1}`
	req := withPeer(relayRequest("/api/federation/relay/profile-page", &pb.RelayProfilePagePayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayProfilePageFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (missing required fields)", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayProfilePageFromPeer_RejectsNonLocalAuthorID(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	// Only the server hosting the author can enumerate their reeds.
	body := `{"author_id":"alice@thirdparty","requester_user_id":"bob@peer5678","page":1}`
	req := withPeer(relayRequest("/api/federation/relay/profile-page", &pb.RelayProfilePagePayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayProfilePageFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (author_id not local to this server)", rr.Code, http.StatusBadRequest)
	}
}

func TestRelayProfilePageFromPeer_NilRelayIsInternalError(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"author_id":"alice@home1234","requester_user_id":"bob@peer5678","page":1}`
	req := withPeer(relayRequest("/api/federation/relay/profile-page", &pb.RelayProfilePagePayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.RelayProfilePageFromPeer(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (realtimeRelay is nil)", rr.Code, http.StatusInternalServerError)
	}
}

// The removal-notify handlers verify the author's detached signature before
// storing a cert, so a peer cannot assert a removal it has no signature for.
// The checks below all run before the key lookup, so no live DB is needed.

func TestReedRemovalFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"cert":{"reed_id":"alice@peer5678/01a026d4","user_id":"alice@peer5678","user_signature":"c2ln","server_signature":"c2ln"}}`
	req := relayRequest("/api/federation/relay/reed-removal", &pb.RelayReedRemovalPayload{}, body)
	rr := httptest.NewRecorder()

	h.ReedRemovalFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no peerServerIDKey in context)", rr.Code, http.StatusUnauthorized)
	}
}

func TestReedRemovalFromPeer_RejectsMissingSignature(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"cert":{"reed_id":"alice@peer5678/01a026d4","user_id":"alice@peer5678","user_signature":"","server_signature":"c2ln"}}`
	req := withPeer(relayRequest("/api/federation/relay/reed-removal", &pb.RelayReedRemovalPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.ReedRemovalFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (user_signature is required)", rr.Code, http.StatusBadRequest)
	}
}

func TestReedRemovalFromPeer_RejectsReedOfAnotherServer(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	// A peer may only report removals for reeds authored on its own server.
	body := `{"cert":{"reed_id":"alice@thirdparty/01a026d4","user_id":"alice@thirdparty","user_signature":"c2ln","server_signature":"c2ln"}}`
	req := withPeer(relayRequest("/api/federation/relay/reed-removal", &pb.RelayReedRemovalPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.ReedRemovalFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (reed_id not local to the calling peer)", rr.Code, http.StatusBadRequest)
	}
}

func TestAccountRemovalNotifyFromPeer_RejectsNonPeerCaller(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"user_id":"alice@peer5678","user_signature":"c2ln","server_signature":"c2ln"}`
	req := relayRequest("/api/federation/removals/account", &pb.RelayAccountRemovalNotifyPayload{}, body)
	rr := httptest.NewRecorder()

	h.AccountRemovalNotifyFromPeer(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (no peerServerIDKey in context)", rr.Code, http.StatusUnauthorized)
	}
}

func TestAccountRemovalNotifyFromPeer_RejectsUserOfAnotherServer(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	body := `{"user_id":"alice@thirdparty","user_signature":"c2ln","server_signature":"c2ln"}`
	req := withPeer(relayRequest("/api/federation/removals/account", &pb.RelayAccountRemovalNotifyPayload{}, body), "peer5678")
	rr := httptest.NewRecorder()

	h.AccountRemovalNotifyFromPeer(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (user_id not local to the calling peer)", rr.Code, http.StatusBadRequest)
	}
}

// The calling peer names the key the holder encrypts to, so it must be a key
// of the requester, who must be one of the peer's own users.
func TestRelayRequestFromPeer_RejectsForeignRequesterKey(t *testing.T) {
	h := newBareRelayTestHandlers("home1234")
	for _, key := range []string{"", "carol@peer5678/k1", "bob@other999/k1", "bob@peer5678"} {
		body := `{"reed_id":"01a026d4","author_id":"alice@home1234","requester_user_id":"bob@peer5678","requester_key_id":"` + key + `","peer_request_id":"bob@peer5678/r1"}`
		req := withPeer(relayRequest("/api/federation/relay/request", &pb.RelayRequestPayload{}, body), "peer5678")
		rr := httptest.NewRecorder()
		h.RelayRequestFromPeer(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("requester_key_id %q: status = %d, want %d", key, rr.Code, http.StatusBadRequest)
		}
	}
}

// relayRequest builds a relay call whose body is js, a protojson fixture,
// encoded as msg.
func relayRequest(path string, msg proto.Message, js string) *http.Request {
	if err := protojson.Unmarshal([]byte(js), msg); err != nil {
		panic(err)
	}
	return protoRequest(http.MethodPost, path, msg)
}

// malformedRelayRequest builds a relay call whose body doesn't decode.
func malformedRelayRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte{0xff, 0xff, 0xff}))
	req.Header.Set("Content-Type", protobufContentType)
	return req
}
