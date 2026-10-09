//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// Cross-server REQUEST_REED relay: bridges the existing local
// relay-holder WebSocket mechanism (realtime/service.go) across a
// federation peer boundary via three peer-to-peer HTTP RPCs. A viewer's
// own server (the "originating" server, O) registers interest in a
// foreign reed with its home server (H) over signed peer HTTP; H runs its
// normal local relay-holder dance; when H's holder relays content back
// over H's own WS, H calls back to O over signed peer HTTP, and O
// delivers to its own local WS client exactly as if a local
// RELAY_RESPONSE had arrived. See the plan for the full design.
//
// None of these three endpoints are simple "proxy the inbound request"
// cases like proxyToPeer — each side originates a new, purpose-built
// signed request with its own JSON body and real two-sided business
// logic, so they're modeled on forwardFollowToPeer's shape instead.

// peerRequestIDMatchesPeer checks that id's embedded serverID (the
// requesterID@serverID/suffix shape every canonical request_id/event_id
// has) equals callerServerID — the peer this HTTP request was
// authenticated as. H has no way to verify WHICH user on O this
// represents (that identity is O's own business, proven to O's own
// client, not to H), so only the server half is checked: an established
// peer can only ever vouch for request ids naming its own server.
func peerRequestIDMatchesPeer(id, callerServerID string) bool {
	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(id))
	if !ok {
		return false
	}
	return embeddedServerID == callerServerID
}

// relayLeg extracts the trailing path segment ("request", "subscribe", ...)
// from a "/api/federation/relay/..." path, for use as a metric attribute —
// every leg name is a literal in this file, never influenced by request data.
func relayLeg(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// callPeerRelayEndpoint POSTs body (nil for none) to a peer's relay RPC
// path, signed as this server's own key, and decodes a 2xx response into
// out (nil to ignore it). Returns the peer's HTTP status.
func (h *Handlers) callPeerRelayEndpoint(ctx context.Context, peerServerID, baseURL, path string, body, out proto.Message) (status int, err error) {
	leg := relayLeg(path)
	ok := false
	defer func() { h.metrics.FederationRelay(ctx, metrics.DirectionOut, peerServerID, leg, ok) }()

	var payload []byte
	if body != nil {
		if payload, err = proto.Marshal(body); err != nil {
			return 0, err
		}
	}
	target := strings.TrimRight(baseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("Content-Type", protobufContentType)
	if err := h.setPeerProxyAuthHeaders(httpReq, string(payload)); err != nil {
		return 0, err
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusForbidden {
		h.acceptRefusalBlock(ctx, peerServerID, respBody)
	}
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := proto.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	return resp.StatusCode, nil
}

// ///////////////////////////////////// //
//   Leg 1: register-request (O -> H)    //
// ///////////////////////////////////// //

// requesterKeyBelongsTo reports whether keyID is a key of userID, a user of
// peerServerID: the calling peer names the key, and only for its own users.
func requesterKeyBelongsTo(keyID, userID, peerServerID string) bool {
	owner, serverID, _, ok := parseKeyFingerprint(identityID(keyID))
	return ok && serverID == peerServerID && string(canonicalID(serverID, owner)) == userID
}

// relayRequestToPeer is HandleForeignRequestReed's hook implementation
// (leg 1, O's side): registers requesterUserID's interest in reedID with
// reedID's home server over peer HTTP.
func (h *Handlers) relayRequestToPeer(ctx context.Context, reedID, requesterUserID, localRequestID string) (realtimeForeignRequestResult, string, error) {
	return h.registerRelayWithPeer(ctx, reedID, requesterUserID, localRequestID, false)
}

// relayThreadRequestToPeer registers a request for the whole thread
// threadID heads with its home server.
func (h *Handlers) relayThreadRequestToPeer(ctx context.Context, threadID, requesterUserID, localRequestID string) (realtimeForeignRequestResult, string, error) {
	return h.registerRelayWithPeer(ctx, threadID, requesterUserID, localRequestID, true)
}

func (h *Handlers) registerRelayWithPeer(ctx context.Context, reedID, requesterUserID, localRequestID string, thread bool) (realtimeForeignRequestResult, string, error) {
	authorUserID, homeServerID, bareReedID, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return realtimeForeignRequestReedNotFound, "", nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	if peer == nil {
		return realtimeForeignRequestReedNotFound, "", nil
	}

	requesterKeyID, err := h.services.db.GetActiveKeyFingerprint(ctx, requesterUserID)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	payload := &pb.RelayRequestPayload{
		ReedId:          bareReedID,
		AuthorId:        string(canonicalID(homeServerID, authorUserID)),
		RequesterUserId: requesterUserID,
		RequesterKeyId:  requesterKeyID,
		PeerRequestId:   localRequestID,
		Thread:          thread,
	}
	var respBody pb.RelayRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/request", payload, &respBody)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	switch {
	case status == http.StatusOK:
		return realtimeForeignRequestOK, respBody.PeerEventId, nil
	case status == http.StatusAccepted:
		return realtimeForeignRequestAccepted, respBody.PeerEventId, nil
	case status == http.StatusNotFound:
		return realtimeForeignRequestReedNotFound, "", nil
	case status == http.StatusConflict:
		return realtimeForeignRequestReedNotHeld, "", nil
	default:
		return realtimeForeignRequestReedNotFound, "", nil
	}
}

// RelayRequestFromPeer is leg 1's home-server handler: an established
// peer is registering a REQUEST_REED on behalf of one of its own users.
func (h *Handlers) RelayRequestFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayRequestPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	req.AuthorId = strings.TrimSpace(req.AuthorId)
	req.RequesterUserId = strings.TrimSpace(req.RequesterUserId)
	req.RequesterKeyId = strings.TrimSpace(req.RequesterKeyId)
	if req.ReedId == "" || req.AuthorId == "" || req.RequesterUserId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeError(w, http.StatusBadRequest, "reed_id, author_id, and requester_user_id are required")
		return
	}
	if !requesterKeyBelongsTo(req.RequesterKeyId, req.RequesterUserId, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeError(w, http.StatusBadRequest, "requester_key_id is not a key of the requester on the calling peer")
		return
	}

	// Loop-prevention: this server can only ever be "home" for reeds it
	// actually authors locally — never chain a request further to a third
	// server. author_id's embedded serverID must be this server's own.
	authorUserID, embeddedServerID, parseOK := parseIdentityID(identityID(req.AuthorId))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeError(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestId, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeError(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}
	canonicalReedID := string(appendEntity(canonicalID(h.services.db.GetServerID(), authorUserID), req.ReedId))

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		internalServerError(w)
		return
	}
	if h.refuseBlockedRequester(w, r, req.AuthorId, req.RequesterUserId) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		return
	}
	var result realtimeForeignRequestResult
	var peerEventID string
	var err error
	if req.Thread {
		result, peerEventID, err = h.realtimeRelay.HandleForeignRequestThread(r.Context(), canonicalReedID, peerServerID, req.RequesterUserId, req.RequesterKeyId)
	} else {
		result, peerEventID, err = h.realtimeRelay.HandleForeignRequestReed(r.Context(), canonicalReedID, peerServerID, req.RequesterUserId, req.RequesterKeyId, req.PeerRequestId)
	}
	if err != nil {
		log.Error().Err(err).Str("reedID", canonicalReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		internalServerError(w)
		return
	}
	switch result {
	case realtimeForeignRequestReedNotFound:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeError(w, http.StatusNotFound, "Reed not found")
	case realtimeForeignRequestReedNotHeld:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeError(w, http.StatusConflict, "Reed is not currently held")
	case realtimeForeignRequestAccepted:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusAccepted, &pb.RelayRequestResponse{PeerEventId: peerEventID, Status: "accepted"})
	default:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusOK, &pb.RelayRequestResponse{PeerEventId: peerEventID, Status: "ack"})
	}
}

// ///////////////////////////////////// //
//   profile-page (O -> H)               //
// ///////////////////////////////////// //
//
// One page of a foreign author's reed ids. The asking server opens each
// one itself and folds it at the border.

// profilePageToPeer is ForeignProfilePageHook's implementation (O's side):
// asks authorID's home server for one page of their reeds. count/hasMore
// come straight from that server, since only it can see the whole list.
func (h *Handlers) profilePageToPeer(ctx context.Context, authorID, requesterUserID string, page int) ([]string, int, bool, error) {
	_, homeServerID, ok := parseIdentityID(identityID(authorID))
	if !ok {
		return nil, 0, false, nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return nil, 0, false, err
	}
	if peer == nil {
		return nil, 0, false, nil
	}

	payload := &pb.RelayProfilePagePayload{AuthorId: authorID, RequesterUserId: requesterUserID, Page: int32(page)}
	var respBody pb.RelayProfilePageResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/profile-page", payload, &respBody)
	if err != nil {
		return nil, 0, false, err
	}
	if status != http.StatusOK {
		return nil, 0, false, nil
	}

	return respBody.ReedIds, int(respBody.Count), respBody.HasMore, nil
}

// RelayProfilePageFromPeer is the home-server handler for a history page:
// an established peer is asking for one page of a local author's reeds on
// behalf of one of its own users.
func (h *Handlers) RelayProfilePageFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayProfilePagePayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.AuthorId = strings.TrimSpace(req.AuthorId)
	if req.AuthorId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeError(w, http.StatusBadRequest, "author_id is required")
		return
	}

	// Loop-prevention: this server can only ever be "home" for authors it
	// actually hosts locally.
	_, embeddedServerID, parseOK := parseIdentityID(identityID(req.AuthorId))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeError(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}
	if _, requesterServerID, ok := parseIdentityID(identityID(req.RequesterUserId)); !ok || requesterServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeError(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		internalServerError(w)
		return
	}
	if h.refuseBlockedRequester(w, r, req.AuthorId, req.RequesterUserId) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", true)
		return
	}
	reedIDs, count, hasMore, err := h.realtimeRelay.HandleForeignProfilePage(r.Context(), req.AuthorId, int(req.Page))
	if err != nil {
		log.Error().Err(err).Str("authorID", req.AuthorId).Str("peerServerID", peerServerID).Msg("Failed to handle foreign profile page")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		internalServerError(w)
		return
	}

	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", true)
	writeResponse(w, http.StatusOK, &pb.RelayProfilePageResponse{ReedIds: reedIDs, Count: int32(count), HasMore: hasMore})
}

// ///////////////////////////////////// //
//   Leg 2: deliver-response (H -> O)    //
// ///////////////////////////////////// //

// deliverRelayResponseToPeer is handleRelayResponse's foreignDeliverHook
// implementation (leg 2, H's side): delivers relayed content back to the
// requesting peer over HTTP instead of a (nonexistent) local WS connection.
func (h *Handlers) deliverRelayResponseToPeer(ctx context.Context, requestingServerID, peerEventID string, data relayedThread) error {
	peer, err := h.services.db.GetServerByID(ctx, requestingServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayDeliverPayload{PeerEventId: peerEventID, Ciphertext: data.Ciphertext}
	if data.Record != nil {
		payload.Record = pbThreadRecord(*data.Record)
	}
	_, err = h.callPeerRelayEndpoint(ctx, requestingServerID, peer.BaseURL, "/api/federation/relay/deliver", payload, nil)
	return err
}

// DeliverRelayResponseFromPeer is leg 2's originating-server handler: the
// home server is delivering relayed content for a request this server
// registered via leg 1.
func (h *Handlers) DeliverRelayResponseFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayDeliverPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventId = strings.TrimSpace(req.PeerEventId)
	if req.PeerEventId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		writeError(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		internalServerError(w)
		return
	}
	// A relayed thread carries its record; the parts count only once verified.
	var threadReedIDs []string
	thread := relayedThread{Ciphertext: req.Ciphertext}
	if req.Record != nil {
		record := threadRecordFromPB(req.Record)
		thread.Record = &record
		ids, err := h.verifyPeerThreadRecord(r.Context(), peerServerID, record)
		if err != nil {
			log.Warn().Err(err).Str("peerEventID", req.PeerEventId).Str("peerServerID", peerServerID).Msg("Relayed thread record failed verification")
		} else {
			threadReedIDs = ids
		}
	}
	found, err := h.realtimeRelay.HandleForeignRelayResponse(r.Context(), req.PeerEventId, peerServerID, thread, threadReedIDs)
	if err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventId).Str("peerServerID", peerServerID).Msg("Failed to handle foreign relay response")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		internalServerError(w)
		return
	}
	if !found {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", true)
		writeError(w, http.StatusNotFound, "Unknown or already-resolved event")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", true)
	writeResponse(w, http.StatusOK, nil)
}

// ///////////////////////////////////////// //
//   Leg 2b: not-held / give-up (H -> O)     //
// ///////////////////////////////////////// //
//
// Failure counterpart of leg 2: H registered O's request (leg 1) but
// exhausted every holder before anyone could relay the content. Without
// this, O's local pending event would sit forever with nothing to notify
// its requester or clean up its own bookkeeping.

// notifyRelayNotHeldToPeer is failReedNotHeld's foreignNotHeldHook
// implementation (H's side): tells the requesting peer this server gave
// up relaying peerEventID.
func (h *Handlers) notifyRelayNotHeldToPeer(ctx context.Context, requestingServerID, peerEventID string) error {
	peer, err := h.services.db.GetServerByID(ctx, requestingServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayNotHeldPayload{PeerEventId: peerEventID}
	_, err = h.callPeerRelayEndpoint(ctx, requestingServerID, peer.BaseURL, "/api/federation/relay/not-held", payload, nil)
	return err
}

// RelayNotHeldFromPeer is leg 2b's originating-server handler: the home
// server is telling us it gave up on a request we registered via leg 1.
func (h *Handlers) RelayNotHeldFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayNotHeldPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventId = strings.TrimSpace(req.PeerEventId)
	if req.PeerEventId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		writeError(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		internalServerError(w)
		return
	}
	found, err := h.realtimeRelay.HandleForeignRelayNotHeld(r.Context(), req.PeerEventId, peerServerID)
	if err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventId).Str("peerServerID", peerServerID).Msg("Failed to handle foreign relay not-held")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		internalServerError(w)
		return
	}
	if !found {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", true)
		writeError(w, http.StatusNotFound, "Unknown or already-resolved event")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", true)
	writeResponse(w, http.StatusOK, nil)
}

// ///////////////////////////////////// //
//   Leg 4: cancel-request (O -> H)      //
// ///////////////////////////////////// //

// cancelRelayRequestWithPeer is the disconnect-cleanup foreignCancelHook
// implementation (leg 4, O's side): tells homeServerID to drop its half
// of a pending request whose originating local requester disconnected.
func (h *Handlers) cancelRelayRequestWithPeer(ctx context.Context, homeServerID, peerEventID string) error {
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayCancelPayload{PeerEventId: peerEventID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/cancel", payload, nil)
	return err
}

// CancelRelayRequestFromPeer is leg 4's home-server handler: the
// originating server's local requester disconnected before delivery
// completed; drop this server's half of the pending state. Idempotent.
func (h *Handlers) CancelRelayRequestFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayCancelPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventId = strings.TrimSpace(req.PeerEventId)
	if req.PeerEventId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeError(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.CancelForeignPendingEvent(r.Context(), req.PeerEventId, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventId).Str("peerServerID", peerServerID).Msg("Failed to cancel foreign pending event")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   Leg 5: ack-delivered (O -> H)       //
// ///////////////////////////////////// //
//
// Closes the loop left open by legs 1-4: once O's viewer verifies
// delivered content and O persists its own local allocation, O tells H
// so H can record that O's server now holds a copy. Without this, H has
// no way to know a relay actually landed, so every future
// profile-subscribe backfill re-offers content the peer already holds —
// this is what makes SUBSCRIBE_PROFILE's cross-server bridge behave like
// the local case (skip what's already held) instead of resending
// everything on every visit. See the holder-notify leg below for the
// distinct, independently-firing notification used by the fallback-fetch
// path.

// ackRelayDeliveryWithPeer is the foreignAckHook implementation (leg 5,
// O's side): tells homeServerID that peerEventID's delivered content was
// verified and locally allocated. O has already persisted its own
// allocation before this call — a failure here only leaves H's
// bookkeeping stale, never loses O's own record of what its viewer holds.
func (h *Handlers) ackRelayDeliveryWithPeer(ctx context.Context, homeServerID, peerEventID string) error {
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayAckPayload{PeerEventId: peerEventID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/ack", payload, nil)
	return err
}

// AckRelayDeliveryFromPeer is leg 5's home-server handler: the
// originating server's local viewer verified and allocated the delivered
// content; record that peer server as a known holder (RecordServerHolder)
// so future GetUnallocatedReeds-style queries for it stop re-offering
// content already successfully relayed.
func (h *Handlers) AckRelayDeliveryFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayAckPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventId = strings.TrimSpace(req.PeerEventId)
	if req.PeerEventId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeError(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignAck(r.Context(), req.PeerEventId, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventId).Str("peerServerID", peerServerID).Msg("Failed to handle foreign ack")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   Leg 8: subscribe-reed (O -> H)      //
// ///////////////////////////////////// //
//
// Live counterpart of the read-proxied GetReed/GetRipples paths: a viewer
// on O wants ongoing stat pushes for one of H's reeds, not just a
// one-time snapshot.

// subscribeReedToPeer is ForeignSubscribeReedHook's implementation (leg
// 8, O's side): registers requesterUserID's interest in reedID's live
// stats with reedID's home server, returning the current snapshot.
func (h *Handlers) subscribeReedToPeer(ctx context.Context, reedID, requesterUserID string) (realtimeForeignReedStatsSnapshot, bool, error) {
	_, homeServerID, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return realtimeForeignReedStatsSnapshot{}, false, nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	if peer == nil {
		return realtimeForeignReedStatsSnapshot{}, false, nil
	}

	payload := &pb.RelaySubscribeReedPayload{ReedId: reedID, RequesterUserId: requesterUserID}
	var respBody pb.RelaySubscribeReedResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/subscribe-reed", payload, &respBody)
	if err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	if status != http.StatusOK || !respBody.Found {
		return realtimeForeignReedStatsSnapshot{}, false, nil
	}

	return realtimeForeignReedStatsSnapshot{
		Echoes:          int(respBody.Echoes),
		CoveragePercent: int(respBody.CoveragePercent),
		Replies:         int(respBody.Replies),
		Likes:           int(respBody.Likes),
	}, true, nil
}

// RelaySubscribeReedFromPeer is leg 8's home-server handler: an
// established peer's viewer wants live stats for one of this server's reeds.
func (h *Handlers) RelaySubscribeReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelaySubscribeReedPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	req.RequesterUserId = strings.TrimSpace(req.RequesterUserId)
	if req.ReedId == "" || req.RequesterUserId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeError(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	// Loop-prevention/spoof guard: this server can only be "home" for
	// reeds it hosts locally, and a peer may only register its own users.
	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedId))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeError(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := parseIdentityID(identityID(req.RequesterUserId))
	if !requesterOK || requesterServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeError(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		internalServerError(w)
		return
	}
	if h.refuseBlockedRequester(w, r, reedAuthorIdentity(req.ReedId), req.RequesterUserId) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", true)
		return
	}
	snapshot, found, err := h.realtimeRelay.HandleForeignSubscribeReed(r.Context(), req.ReedId, peerServerID, req.RequesterUserId)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed stats subscription")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", true)
	writeResponse(w, http.StatusOK, &pb.RelaySubscribeReedResponse{
		Found:           found,
		Echoes:          int32(snapshot.Echoes),
		CoveragePercent: int32(snapshot.CoveragePercent),
		Replies:         int32(snapshot.Replies),
		Likes:           int32(snapshot.Likes),
	})
}

// ///////////////////////////////////////// //
//   Leg 9: unsubscribe-reed (O -> H)        //
// ///////////////////////////////////////// //

// unsubscribeReedWithPeer is the foreignUnsubscribeReedHook
// implementation (leg 9, O's side): tells reedID's home server that
// requesterUserID no longer wants live stats.
func (h *Handlers) unsubscribeReedWithPeer(ctx context.Context, reedID, requesterUserID string) error {
	_, homeServerID, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayUnsubscribeReedPayload{ReedId: reedID, RequesterUserId: requesterUserID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/unsubscribe-reed", payload, nil)
	return err
}

// RelayUnsubscribeReedFromPeer is leg 9's home-server handler.
func (h *Handlers) RelayUnsubscribeReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayUnsubscribeReedPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	req.RequesterUserId = strings.TrimSpace(req.RequesterUserId)
	if req.ReedId == "" || req.RequesterUserId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeError(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedId))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeError(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := parseIdentityID(identityID(req.RequesterUserId))
	if !requesterOK || requesterServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeError(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignUnsubscribeReed(r.Context(), req.ReedId, req.RequesterUserId); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("requesterUserID", req.RequesterUserId).Msg("Failed to handle foreign reed stats unsubscribe")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   Leg 10: reed-stats push (H -> O)    //
// ///////////////////////////////////// //
//
// Delivery half of the live reed-stats bridge: H pushes an already-built
// WS message (coverage/echoes/replies/likes/ripple-posted/ripple-updated)
// straight to O, which relays it unmodified to its own local client —
// same "server just delivers, client verifies whatever needs verifying"
// split used everywhere else in this file.

// pushReedStatsToPeer sends one live update for reedID to a peer, once for
// all of its viewers. The status tells the caller whether the peer still
// has anyone subscribed.
func (h *Handlers) pushReedStatsToPeer(ctx context.Context, peerServerID, reedID, excludeUserID string, msg *pb.WSMessage) (int, error) {
	peer, err := h.services.db.GetServerByID(ctx, peerServerID)
	if err != nil {
		return 0, err
	}
	if peer == nil {
		return http.StatusNotFound, nil
	}
	body := &pb.RelayReedStatsPayload{ReedId: reedID, ExcludeUserId: excludeUserID, Message: msg}
	return h.callPeerRelayEndpoint(ctx, peerServerID, peer.BaseURL, "/api/federation/relay/reed-stats", body, nil)
}

// PushReedStatsFromPeer forwards a live update for one of the peer's reeds
// to this server's users subscribed to it. 404 means nobody here is, so
// the peer can drop its subscriptions for us.
func (h *Handlers) PushReedStatsFromPeer(w http.ResponseWriter, r *http.Request) {
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayReedStatsPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	if req.ReedId == "" || req.Message == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeError(w, http.StatusBadRequest, "reed_id and message are required")
		return
	}
	// A peer only reports on its own reeds.
	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedId))
	if !reedOK || reedServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeError(w, http.StatusBadRequest, "reed_id does not belong to the calling peer")
		return
	}
	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		internalServerError(w)
		return
	}

	delivered := h.realtimeRelay.DeliverForeignReedStats(r.Context(), req.ReedId, req.ExcludeUserId, req.Message)
	if !delivered {
		writeError(w, http.StatusNotFound, "No subscribers for this reed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////////// //
//   Leg 15: holder-notify (H' -> H)         //
// ///////////////////////////////////////// //
//
// H' (a cache-holder, not reedID's home) tells reedID's actual home server
// H that H' now holds a verified copy — fired whenever a local client acks
// a foreign reed, regardless of which delivery path brought it. This is
// the fact leg 16 (fallback-fetch) later relies on: without it, H has no
// way to learn any peer holds a copy of one of its own reeds, so a local
// requester with no online local holder has nothing to fall back to.
// Fire-and-forget: H''s own allocation is already persisted before this
// call, so a lost/failed notification only leaves H's fallback-routing
// table stale, never loses H''s record of what it holds.

// notifyHolderToPeer is the ForeignHolderNotifyHook implementation: tells
// homeServerID that this server now holds a copy of reedID.
func (h *Handlers) notifyHolderToPeer(ctx context.Context, homeServerID, reedID string) error {
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := &pb.RelayHolderNotifyPayload{ReedId: reedID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/holder-notify", payload, nil)
	return err
}

// HolderNotifyFromPeer is leg 15's home-server handler: an established
// peer is telling us it holds a copy of one of our reeds. No ownership
// check beyond "caller is an authenticated peer" is needed — recording
// "peer X holds reed R" doesn't require R to be owned by X, unlike leg 16
// below, which does.
func (h *Handlers) HolderNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayHolderNotifyPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	if req.ReedId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		writeError(w, http.StatusBadRequest, "reed_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleHolderNotify(r.Context(), req.ReedId, peerServerID); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Failed to handle holder notify")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////////// //
//   Leg 16: fallback-request (H -> H')      //
// ///////////////////////////////////////// //
//
// H (reedID's actual home) found no online local holder, but knows —
// via leg 15 — that peer H' previously held a copy, and asks H' to relay
// it back for one of H's own local users. Same two-step register/deliver
// shape as leg 1/2, because H''s own delivery to its holder is inherently
// asynchronous (H' must RELAY_REQUEST its own online holder over
// WebSocket and wait for a separate RELAY_RESPONSE) — H' can only
// synchronously accept or reject the registration here; the actual
// content arrives via the existing, unmodified leg-2 deliver callback
// once H''s holder responds. The ownership check here is the INVERSE of
// leg 1's: leg 1 requires the callee to be reedID's home; this requires
// the CALLER to be, since H' is being asked to hand back content on
// behalf of a reed it doesn't own.

// relayFallbackRequestToPeer is the ForeignFallbackRequestHook
// implementation: asks peerServerID — a server previously notified (leg
// 15) that it holds a copy of reedID — to relay that copy back to
// requesterUserID, one of this server's own local users.
func (h *Handlers) relayFallbackRequestToPeer(ctx context.Context, peerServerID, reedID, requesterUserID, localRequestID string) (realtimeForeignRequestResult, string, error) {
	peer, err := h.services.db.GetServerByID(ctx, peerServerID)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	if peer == nil {
		return realtimeForeignRequestReedNotFound, "", nil
	}

	requesterKeyID, err := h.services.db.GetActiveKeyFingerprint(ctx, requesterUserID)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	payload := &pb.RelayFallbackRequestPayload{
		ReedId:          reedID,
		RequesterUserId: requesterUserID,
		RequesterKeyId:  requesterKeyID,
		PeerRequestId:   localRequestID,
	}
	var respBody pb.RelayFallbackRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, peerServerID, peer.BaseURL, "/api/federation/relay/fallback-request", payload, &respBody)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	switch {
	case status == http.StatusOK:
		return realtimeForeignRequestOK, respBody.PeerEventId, nil
	case status == http.StatusAccepted:
		return realtimeForeignRequestAccepted, respBody.PeerEventId, nil
	case status == http.StatusNotFound:
		return realtimeForeignRequestReedNotFound, "", nil
	case status == http.StatusConflict:
		return realtimeForeignRequestReedNotHeld, "", nil
	default:
		return realtimeForeignRequestReedNotFound, "", nil
	}
}

// RelayFallbackRequestFromPeer is leg 16's handler, run on the server
// previously notified (leg 15) that it holds a cached copy: an
// established peer — reedID's actual home — is asking for that copy back
// on behalf of one of its own local users.
func (h *Handlers) RelayFallbackRequestFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayFallbackRequestPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	req.RequesterUserId = strings.TrimSpace(req.RequesterUserId)
	req.RequesterKeyId = strings.TrimSpace(req.RequesterKeyId)
	if req.ReedId == "" || req.RequesterUserId == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeError(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}
	if !requesterKeyBelongsTo(req.RequesterKeyId, req.RequesterUserId, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeError(w, http.StatusBadRequest, "requester_key_id is not a key of the requester on the calling peer")
		return
	}

	// Inverse of leg 1's loop-prevention: the CALLER must own reedID —
	// this stops any peer from asking us to hand back content on behalf
	// of a reed it doesn't actually author.
	_, embeddedServerID, _, parseOK := parseKeyFingerprint(identityID(req.ReedId))
	if !parseOK || embeddedServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeError(w, http.StatusBadRequest, "reed_id is not owned by the calling peer")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestId, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeError(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		internalServerError(w)
		return
	}
	result, peerEventID, err := h.realtimeRelay.HandleForeignFallbackRequest(r.Context(), req.ReedId, peerServerID, req.RequesterUserId, req.RequesterKeyId, req.PeerRequestId)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Failed to handle fallback request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		internalServerError(w)
		return
	}
	switch result {
	case realtimeForeignRequestReedNotFound:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeError(w, http.StatusNotFound, "Reed not found")
	case realtimeForeignRequestReedNotHeld:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeError(w, http.StatusConflict, "Reed is not currently held")
	case realtimeForeignRequestAccepted:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusAccepted, &pb.RelayFallbackRequestResponse{PeerEventId: peerEventID, Status: "accepted"})
	default:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusOK, &pb.RelayFallbackRequestResponse{PeerEventId: peerEventID, Status: "ack"})
	}
}

// //////////////////////////////////////// //
//   Leg 19: search-users (O -> H)           //
// //////////////////////////////////////// //
//
// Read side of federated @-mention search: the composer's picker only
// ever searched this server's own users, since GET /users/search's query
// only reaches the local `users` table. This leg asks every connected
// peer to run that same local search against ITS users, so results merge
// across the whole known mesh — a peer only ever answers for its own
// local users, same trust boundary as every other leg here. Unlike the
// notify legs above, this is synchronous request/response on the
// caller's critical path (a live user typing into a search box), so the
// caller (SearchUsers in handlers.go) fans out to all peers in parallel
// with a short per-peer timeout and returns whatever answered in time —
// a slow or unreachable peer is dropped for that request, never blocks
// or fails the local results.

// searchUsersFromPeer asks one peer to run its own local user search —
// the single-peer primitive fanoutUserSearchToPeers calls concurrently
// for every connected peer.
func (h *Handlers) searchUsersFromPeer(ctx context.Context, peer PeerServer, query, after string, limit int) ([]UserSearchResult, error) {
	payload := &pb.RelaySearchUsersPayload{Query: query, Limit: int32(limit), After: after}
	var respBody pb.RelaySearchUsersResponse
	status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/search-users", payload, &respBody)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("peer returned status %d", status)
	}
	results := make([]UserSearchResult, 0, len(respBody.Users))
	for _, u := range respBody.Users {
		results = append(results, UserSearchResult{ID: u.GetId(), Username: u.GetUsername(), ServerName: u.GetServerName()})
	}
	return results, nil
}

// fanoutUserSearchToPeers queries every connected peer in parallel, each
// bounded by perPeerTimeout, and returns the concatenation of whatever
// answered in time. A peer that errors or times out is silently dropped —
// this is a best-effort widening of local search results, not a
// completeness guarantee, so one flaky peer must never hold up or fail
// the request.
func (h *Handlers) fanoutUserSearchToPeers(ctx context.Context, query string, after *userSearchCursor, limit int, perPeerTimeout time.Duration) []UserSearchResult {
	peers, err := h.services.db.ListConnectedPeers(ctx)
	if err != nil || len(peers) == 0 {
		return nil
	}
	// Peers only need the position; the lead is ours to skip.
	var peerAfter string
	if after != nil {
		peerAfter = encodeUserSearchCursor(userSearchCursor{Username: after.Username, ID: after.ID})
	}

	var mu sync.Mutex
	var results []UserSearchResult
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(peer PeerServer) {
			defer wg.Done()
			peerCtx, cancel := context.WithTimeout(ctx, perPeerTimeout)
			defer cancel()
			peerResults, err := h.searchUsersFromPeer(peerCtx, peer, query, peerAfter, limit)
			if err != nil {
				return
			}
			// A peer answers only for its own users, past the cursor.
			peerResults = slices.DeleteFunc(peerResults, func(u UserSearchResult) bool {
				_, serverID, ok := parseIdentityID(identityID(u.ID))
				return !ok || serverID != peer.ID || (after != nil && !userSearchAfter(u, *after))
			})
			peerResults = peerResults[:min(len(peerResults), limit+1)]
			mu.Lock()
			results = append(results, peerResults...)
			mu.Unlock()
		}(peer)
	}
	wg.Wait()
	return results
}

// SearchUsersFromPeer is leg 19's home-server handler: an established
// peer is asking us to search our own local users on its behalf.
func (h *Handlers) SearchUsersFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelaySearchUsersPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	var after *userSearchCursor
	if req.After != "" {
		c, err := decodeUserSearchCursor(req.After)
		if err != nil {
			log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Invalid search-users cursor from peer")
			h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", false)
			writeError(w, http.StatusBadRequest, "Invalid after cursor")
			return
		}
		after = c
	}
	if req.Query == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", true)
		writeResponse(w, http.StatusOK, &pb.RelaySearchUsersResponse{})
		return
	}

	// No self-exclusion here: the searching user is on the peer's own
	// server, never a local u.id on this one.
	results, err := h.services.db.SearchUsers(r.Context(), req.Query, "", after, clampUserSearchLimit(int(req.Limit))+1)
	if err != nil {
		log.Error().Err(err).Str("query", req.Query).Str("peerServerID", peerServerID).Msg("Failed to handle foreign search-users request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", true)
	writeResponse(w, http.StatusOK, &pb.RelaySearchUsersResponse{Users: pbUserSearch(results).Users})
}

// notifyPeerOfApproval tells serverID we are approving it. A 2xx means it
// approved us too; a 403 means it hasn't yet. Anything else is an error,
// and the approval must not stand.
func (h *Handlers) notifyPeerOfApproval(ctx context.Context, serverID, baseURL string) (bool, error) {
	if h.approvalNotifierOverride != nil {
		return h.approvalNotifierOverride(ctx, serverID, baseURL)
	}
	status, err := h.callPeerRelayEndpoint(ctx, serverID, baseURL, "/api/federation/relay/approved-notify", nil, nil)
	if err != nil {
		return false, err
	}
	switch {
	case status >= 200 && status < 300:
		return true, nil
	case status == http.StatusForbidden:
		return false, nil
	default:
		return false, fmt.Errorf("peer answered %d", status)
	}
}

// ApprovedNotifyFromPeer handles a peer telling us it approved us. Peer
// auth already recorded that, so there is nothing left to do.
func (h *Handlers) ApprovedNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "approved-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// notifyPeerOfDisconnect tells serverID's own server that this server
// just revoked it locally, so it can revoke this server back instead of
// continuing to trust a peer that already cut ties. Best-effort: the
// caller's own revoke has already committed before this runs, and a
// failure here (peer offline, hostile, unreachable) must not undo it.
func (h *Handlers) notifyPeerOfDisconnect(ctx context.Context, serverID, reason string) error {
	baseURL, err := h.services.db.GetServerBaseURLAnyState(ctx, serverID)
	if err != nil {
		return err
	}
	if baseURL == "" {
		return nil
	}
	payload := &pb.RelayDisconnectNotifyPayload{Reason: reason}
	_, err = h.callPeerRelayEndpoint(ctx, serverID, baseURL, "/api/federation/relay/disconnect-notify", payload, nil)
	return err
}

// DisconnectNotifyFromPeer handles an established peer telling us it just
// revoked us on its side. Revoke it back here too — no human on this
// server made this call, so revoked_by stays NULL.
func (h *Handlers) DisconnectNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayDisconnectNotifyPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "disconnect-notify", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	reason := fmt.Sprintf("Peer-initiated disconnect: %s", strings.TrimSpace(req.Reason))
	err := h.services.db.RevokeFederationServer(r.Context(), peerServerID, nil, reason, time.Now().UTC().Truncate(time.Second))
	if err != nil && !errors.Is(err, errFederationServerAlreadyRevoked) {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("failed to auto-revoke peer on disconnect notify")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "disconnect-notify", false)
		internalServerError(w)
		return
	}
	if h.realtimeRelay != nil {
		h.realtimeRelay.forgetPeer(r.Context(), peerServerID)
	}
	if err := h.services.db.DeletePeerStreams(r.Context(), peerServerID); err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("failed to drop delivery streams of revoked peer")
	}
	if err := h.services.db.DeleteVouchReferencesForServer(r.Context(), peerServerID); err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("failed to drop vouch references of revoked peer")
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "disconnect-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   new-reed and reed-removal: to every peer   //
// ///////////////////////////////////// //

// NewReedFromPeer handles a peer announcing a reed one of its users just
// posted. This server records what concerns it (mentions of its users,
// replies to and echoes of its reeds) and dispatches to its own users.
func (h *Handlers) NewReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fail := func(status int, msg string) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "new-reed", false)
		writeError(w, status, msg)
	}

	var req pb.RelayNewReedPayload
	if err := readRequest(r, &req); err != nil {
		fail(http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedId = strings.TrimSpace(req.ReedId)
	req.AuthorId = strings.TrimSpace(req.AuthorId)
	authorBareID, reedServerID, _, ok := parseKeyFingerprint(identityID(req.ReedId))
	if !ok || reedServerID != peerServerID || string(canonicalID(reedServerID, authorBareID)) != req.AuthorId {
		fail(http.StatusBadRequest, "reed_id and author_id must belong to the calling peer")
		return
	}
	if req.Reply != nil && req.Echo != nil {
		fail(http.StatusBadRequest, "a reed is a reply or an echo, not both")
		return
	}
	if req.Reply != nil && (req.Reply.ParentReedId == "" || req.Reply.RootId == "") {
		fail(http.StatusBadRequest, "reply needs parent_reed_id and root_id")
		return
	}
	if req.Echo != nil && req.Echo.EchoedReedId == "" {
		fail(http.StatusBadRequest, "echo needs echoed_reed_id")
		return
	}
	if h.realtimeRelay == nil {
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	if err := h.receiveForeignNewReed(r.Context(), peerServerID, &req); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Failed to receive foreign new reed")
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "new-reed", true)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) receiveForeignNewReed(ctx context.Context, peerServerID string, req *pb.RelayNewReedPayload) error {
	db := h.services.db
	self := db.GetServerID()
	if err := db.UpsertRemoteIdentity(ctx, req.AuthorId, peerServerID); err != nil {
		return err
	}
	if err := db.UpsertReedIdentity(ctx, req.ReedId); err != nil {
		return err
	}

	for _, mentioned := range req.Mentions {
		bareID, serverID, ok := parseIdentityID(identityID(strings.TrimSpace(mentioned)))
		if !ok || serverID != self {
			continue
		}
		valid, err := db.MentionTargetValid(ctx, bareID, serverID)
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		if err := db.InsertMentionRow(ctx, req.ReedId, string(canonicalID(serverID, bareID))); err != nil {
			return err
		}
	}

	foreignParentID := ""
	if req.Reply != nil {
		_, parentServerID, _, ok := parseKeyFingerprint(identityID(req.Reply.ParentReedId))
		switch {
		case !ok:
		case parentServerID == self:
			// Records the reply and notifies the thread's local viewers.
			if err := h.realtimeRelay.HandleForeignReplyNotify(ctx, req.Reply.ParentReedId, req.ReedId, req.Reply.RootId, timeFromUnix(req.SignedAt)); err != nil {
				return err
			}
		default:
			foreignParentID = req.Reply.ParentReedId
		}
	}

	if req.Echo != nil {
		echoedAuthorBareID, echoedServerID, bareEchoedReedID, ok := parseKeyFingerprint(identityID(req.Echo.EchoedReedId))
		if ok && echoedServerID == self {
			echoedAuthorID := string(canonicalID(echoedServerID, echoedAuthorBareID))
			if err := db.InsertForeignEcho(ctx, req.ReedId, req.Echo.EchoedReedId, req.AuthorId, echoedAuthorID, req.Echo.IsBlank, timeFromUnix(req.SignedAt)); err != nil {
				return err
			}
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
				UserID: echoedAuthorID,
				ReedID: bareEchoedReedID,
			}
		}
	}

	h.realtimeRelay.dispatchForeignNewReed(ctx, req.ReedId, req.AuthorId, foreignParentID)
	return nil
}

// ReedRemovalFromPeer handles a peer removing one of its users' reeds.
// Besides the removal itself it drops any reply or echo reference this
// server kept for the reed and tells the affected thread's viewers.
func (h *Handlers) ReedRemovalFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayReedRemovalPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	cert, status, msg := h.acceptForeignReedRemoval(r.Context(), peerServerID, req.GetCert())
	if status != 0 {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		writeError(w, status, msg)
		return
	}
	if err := h.dropForeignReedReferences(r.Context(), peerServerID, cert.ReedID, req.ParentReedId, cert); err != nil {
		log.Error().Err(err).Str("reedID", cert.ReedID).Msg("Failed to drop references of removed foreign reed")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		internalServerError(w)
		return
	}
	if h.realtimeRelay != nil {
		wire := newReedRemovalWire(peerServerID, cert)
		h.realtimeRelay.HandleForeignReedRemoval(cert.UserID, cert.ReedID, &wire)
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", true)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) dropForeignReedReferences(ctx context.Context, peerServerID, reedID, parentReedID string, cert reedRemovalCert) error {
	db := h.services.db
	self := db.GetServerID()

	if parentReedID == "" {
		parent, ok, err := db.ReplyParent(ctx, reedID)
		if err != nil {
			return err
		}
		if ok {
			parentReedID = parent
		}
	}
	if parentReedID != "" {
		deleted, err := db.DeleteForeignReplyReference(ctx, reedID)
		if err != nil {
			return err
		}
		_, parentServerID, _, _ := parseKeyFingerprint(identityID(parentReedID))
		if deleted && parentServerID == self {
			targets, err := db.ReplyCountNotifyTargets(ctx, parentReedID)
			if err != nil {
				return err
			}
			for _, t := range targets {
				h.broadcastChan <- realtimeBroadcastMessage{
					Type:   realtimeReplyCountChanged,
					UserID: t.CanonicalAuthorID(),
					ReedID: t.ReedID,
				}
			}
		}
		if h.realtimeRelay != nil {
			wire := newReedRemovalWire(peerServerID, cert)
			h.realtimeRelay.HandleForeignReplyRemovalAtParent(parentReedID, reedID, &wire)
		}
	}

	echoedReedID, err := db.DeleteForeignEchoReference(ctx, reedID)
	if err != nil {
		return err
	}
	if echoedReedID != "" {
		echoedAuthorBareID, echoedServerID, bareEchoedReedID, ok := parseKeyFingerprint(identityID(echoedReedID))
		if ok && echoedServerID == self {
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
				UserID: string(canonicalID(echoedServerID, echoedAuthorBareID)),
				ReedID: bareEchoedReedID,
			}
		}
	}
	return nil
}

// ///////////////////////////////////////// //
//   realtime-reset: server going down or up   //
// ///////////////////////////////////////// //

const (
	realtimeResetShutdown = "shutdown"
	realtimeResetBoot     = "boot"
)

// realtimeResetTimeout bounds the whole fan-out, so a dead peer can't
// hold up this server's shutdown.
const realtimeResetTimeout = 5 * time.Second

// notifyPeersOfRealtimeReset tells every connected peer to forget all
// realtime state tied to this server. reason is shutdown or boot; boot
// covers a crash that never sent the shutdown notice.
func (h *Handlers) notifyPeersOfRealtimeReset(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), realtimeResetTimeout)
	defer cancel()
	log := h.services.log.GetLogger(ctx)

	peers, err := h.services.db.ListConnectedPeers(ctx)
	if err != nil {
		log.Error().Err(err).Str("reason", reason).Msg("Failed to list peers for realtime reset")
		return
	}
	payload := &pb.RelayRealtimeResetPayload{Reason: reason}
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(peer PeerServer) {
			defer wg.Done()
			status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/realtime-reset", payload, nil)
			if err == nil && (status < 200 || status >= 300) {
				err = fmt.Errorf("peer answered %d", status)
			}
			// Written inline, not async: on shutdown the process may exit
			// before a background write lands.
			level, message := federationLogInfo, fmt.Sprintf("Sent %s notice", reason)
			if err != nil {
				log.Warn().Err(err).Str("peerServerID", peer.ID).Str("reason", reason).Msg("Failed to send realtime reset to peer")
				level, message = federationLogError, fmt.Sprintf("Failed to send %s notice: %v", reason, err)
			}
			if err := h.services.db.logFederationServer(context.Background(), peer.ID, level, message); err != nil {
				log.Error().Err(err).Str("peerServerID", peer.ID).Msg("Failed to write federation server log")
			}
		}(peer)
	}
	wg.Wait()
}

// RealtimeResetFromPeer handles a peer announcing it is going down or has
// just come up. Either way nothing it held for us survives, so we drop
// everything shared with it; shutdown also marks it down until it boots.
func (h *Handlers) RealtimeResetFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayRealtimeResetPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Reason != realtimeResetShutdown && req.Reason != realtimeResetBoot {
		log.Error().Str("peerServerID", peerServerID).Str("reason", req.Reason).Msg("realtime reset with unknown reason")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		writeError(w, http.StatusBadRequest, "`reason` must be shutdown or boot")
		return
	}
	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		internalServerError(w)
		return
	}

	h.logFederationServerAsync(peerServerID, federationLogInfo, fmt.Sprintf("Received %s notice", req.Reason))
	h.realtimeRelay.forgetPeer(r.Context(), peerServerID)
	if err := h.services.db.SetPeerDown(r.Context(), peerServerID, req.Reason == realtimeResetShutdown); err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to record peer up/down state")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		internalServerError(w)
		return
	}
	// Back up: send what piled up for it while it was down.
	if req.Reason == realtimeResetBoot {
		go h.deliverBehindStreams(peerServerID)
		go h.sendOwedKeyRevocations(peerServerID)
		go h.sendOwedBlockNotices(peerServerID)
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", true)
	w.WriteHeader(http.StatusNoContent)
}

// account-removal-notify: a removed user's own home server notifies every
// peer known to hold a copy of that user's content — not followers or
// subscribers, which are a peer-local concern resolved once it has the cert.

// notifyForeignAccountRemovalToPeers tells every peer holding a copy of
// removedUserID's content that the account was removed, so each can store
// the cert and fan it out to its own local followers/subscribers.
// Best-effort per peer: one unreachable peer must not block the rest.
func (h *Handlers) notifyForeignAccountRemovalToPeers(ctx context.Context, removedUserID string, cert accountRemovalCert) {
	log := h.services.log.GetLogger(ctx)
	serverIDs, err := h.services.db.GetForeignHolderServersForAuthor(ctx, removedUserID)
	if err != nil {
		log.Error().Err(err).Str("userID", removedUserID).Msg("Failed to resolve holder servers for account removal notify")
		return
	}
	payload := &pb.RelayAccountRemovalNotifyPayload{
		UserId:            removedUserID,
		Note:              cert.Note,
		UserSignature:     cert.UserSignature,
		UserKeyId:         cert.UserKeyID,
		ServerSignature:   cert.ServerSignature,
		ServerFingerprint: cert.ServerFingerprint,
		ServerSignedAt:    unixOrZero(cert.ServerSignedAt),
	}
	for _, serverID := range serverIDs {
		peer, err := h.services.db.GetServerByID(ctx, serverID)
		if err != nil || peer == nil {
			continue
		}
		if _, err := h.callPeerRelayEndpoint(ctx, serverID, peer.BaseURL, "/api/federation/relay/account-removal-notify", payload, nil); err != nil {
			log.Error().Err(err).Str("userID", removedUserID).Str("peerServerID", serverID).Msg("Failed to notify peer of account removal")
		}
	}
}

// AccountRemovalNotifyFromPeer: an established peer holding a copy of one
// of its own users' content is telling us that user's account was
// removed. Stores the cert (no local-account side effects — this user was
// never ours) and fans it out to local followers/subscribers exactly like
// a same-server removal would.
func (h *Handlers) AccountRemovalNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RelayAccountRemovalNotifyPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.UserId = strings.TrimSpace(req.UserId)
	if req.UserId == "" || req.UserSignature == "" || req.ServerSignature == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeError(w, http.StatusBadRequest, "user_id, user_signature, and server_signature are required")
		return
	}

	// Loop-prevention: a peer may only notify us about removal of one of
	// its OWN users — never claim removal on behalf of a third server.
	_, userServerID, userOK := parseIdentityID(identityID(req.UserId))
	if !userOK || userServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeError(w, http.StatusBadRequest, "user_id does not belong to the calling peer")
		return
	}

	if err := h.services.db.UpsertRemoteIdentity(r.Context(), req.UserId, peerServerID); err != nil {
		log.Error().Err(err).Str("userID", req.UserId).Msg("Failed to upsert remote identity for account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}

	// account_removals.public_key_id is a hard FK — a foreign user's key
	// is fetched and cached from its owning peer if we don't hold it yet
	// (same resolvePublicKey path LikeReed uses for the same problem).
	pubKey, err := h.resolvePublicKey(r.Context(), req.UserKeyId)
	if err != nil {
		log.Error().Err(err).Str("userID", req.UserId).Str("userKeyID", req.UserKeyId).Msg("Failed to resolve signing key for account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}
	if pubKey == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeError(w, http.StatusBadRequest, "user_key_id could not be resolved")
		return
	}

	// The author signs against their own home server's id, which for a peer
	// notification is the calling peer.
	userSigArmor := req.UserSignature
	userPayload := buildAccountRemovalUserPayload(peerServerID, req.UserId, req.Note)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Err(err).Str("userID", req.UserId).Str("peerServerID", peerServerID).Msg("Account-removal user signature verification failed")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeError(w, http.StatusBadRequest, "user_signature verification failed")
		return
	}

	cert := accountRemovalCert{
		UserID:            req.UserId,
		Note:              req.Note,
		UserSignature:     req.UserSignature,
		UserKeyID:         req.UserKeyId,
		ServerSignature:   req.ServerSignature,
		ServerFingerprint: req.ServerFingerprint,
		ServerSignedAt:    timeFromUnix(req.ServerSignedAt),
	}
	if err := h.services.db.InsertForeignAccountRemoval(r.Context(), cert); err != nil && !errors.Is(err, errRemovalConflict) {
		log.Error().Err(err).Str("userID", req.UserId).Str("peerServerID", peerServerID).Msg("Failed to store foreign account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}

	if h.realtimeRelay != nil {
		wire := newAccountRemovalWire(peerServerID, cert)
		h.realtimeRelay.HandleForeignAccountRemoval(req.UserId, &wire)
	}

	log.Info().Str("userID", req.UserId).Str("peerServerID", peerServerID).Msg("Stored foreign account removal")
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// pb.RelayReedRemovalCert is a signed reed removal as it travels between
// servers, inside reed-removal.

// acceptForeignReedRemoval verifies a peer's reed removal and stores the
// cert. A non-zero status is the HTTP answer for a rejected removal.
func (h *Handlers) acceptForeignReedRemoval(ctx context.Context, peerServerID string, req *pb.RelayReedRemovalCert) (reedRemovalCert, int, string) {
	log := h.services.log.GetLogger(ctx)
	req.ReedId = strings.TrimSpace(req.ReedId)
	if req.ReedId == "" || req.UserId == "" || req.UserSignature == "" || req.ServerSignature == "" {
		return reedRemovalCert{}, http.StatusBadRequest, "reed_id, user_id, user_signature, and server_signature are required"
	}

	// Loop-prevention: a peer may only notify us about removal of a reed
	// authored by one of its OWN users — never claim removal on behalf of
	// a third server.
	_, authorServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedId))
	if !reedOK || authorServerID != peerServerID {
		return reedRemovalCert{}, http.StatusBadRequest, "reed_id does not belong to the calling peer"
	}

	if author, ok := authorOf(identityID(req.ReedId)); !ok || string(author) != req.UserId ||
		!requesterKeyBelongsTo(req.UserKeyId, req.UserId, peerServerID) {
		return reedRemovalCert{}, http.StatusBadRequest, "user_key_id is not a key of the reed's author"
	}

	pubKey, err := h.resolvePublicKey(ctx, req.UserKeyId)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("userKeyID", req.UserKeyId).Msg("Failed to resolve signing key for reed removal")
		return reedRemovalCert{}, http.StatusInternalServerError, "Internal Server Error"
	}
	if pubKey == nil {
		return reedRemovalCert{}, http.StatusBadRequest, "user_key_id could not be resolved"
	}

	// The author signs against their own home server's id, which for a peer
	// notification is the calling peer.
	userSigArmor := req.UserSignature
	userPayload := buildReedRemovalUserPayload(peerServerID, req.ReedId)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Reed-removal user signature verification failed")
		return reedRemovalCert{}, http.StatusBadRequest, "user_signature verification failed"
	}

	signedAt := timeFromUnix(req.ServerSignedAt)
	if err := h.verifyPeerCountersignature(ctx, peerServerID, ServerSignature{
		ID: req.ServerFingerprint, Armor: req.ServerSignature, SignedAt: signedAt,
	}, func(serverFP string) []byte {
		return buildReedRemovalServerPayload(peerServerID, req.ReedId, req.UserKeyId, serverFP, req.UserSignature, signedAt)
	}); err != nil {
		log.Warn().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Reed-removal countersignature verification failed")
		return reedRemovalCert{}, http.StatusBadRequest, "server_signature verification failed"
	}

	cert := reedRemovalCert{
		ReedID:            req.ReedId,
		UserID:            req.UserId,
		UserSignature:     req.UserSignature,
		UserKeyID:         req.UserKeyId,
		ServerSignature:   req.ServerSignature,
		ServerFingerprint: req.ServerFingerprint,
		ServerSignedAt:    timeFromUnix(req.ServerSignedAt),
	}
	if err := h.services.db.InsertReedRemoval(ctx, cert); err != nil && !errors.Is(err, errRemovalConflict) {
		log.Error().Err(err).Str("reedID", req.ReedId).Str("peerServerID", peerServerID).Msg("Failed to store foreign reed removal")
		return reedRemovalCert{}, http.StatusInternalServerError, "Internal Server Error"
	}
	return cert, 0, ""
}

// ///////////////////////////////////////// //
//   server-key: following a peer's new key    //
// ///////////////////////////////////////// //

// peerKeyUpdateRetry spaces out revocation fetches for the same unknown peer key,
// so a burst of requests signed with it costs the peer one round.
const peerKeyUpdateRetry = 5 * time.Minute

// ServerKeyNoticeFromPeer answers a peer's new-key notice. The work happens
// in authentication, which re-pins an unknown key before this runs.
func (h *Handlers) ServerKeyNoticeFromPeer(w http.ResponseWriter, r *http.Request) {
	if peerServerID, ok := r.Context().Value(peerServerIDKey).(string); !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updatePeerKey follows an established peer's key revocations when it signs with
// a key other than the one pinned for it, and reports whether the pin moved.
// A compromised revocation clears the pin until an admin re-approves.
func (h *Handlers) updatePeerKey(ctx context.Context, peerServerID, fingerprint string) bool {
	log := h.services.log.GetLogger(ctx)
	attempt := peerServerID + "/" + fingerprint
	if last, seen := h.peerKeyUpdateAttempts.Load(attempt); seen && time.Since(last.(time.Time)) < peerKeyUpdateRetry {
		return false
	}
	h.peerKeyUpdateAttempts.Store(attempt, time.Now())

	pin, err := h.services.db.GetPeerPin(ctx, peerServerID)
	if err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to load peer pin")
		return false
	}
	if pin == nil || pin.KeyID == string(canonicalID(peerServerID, fingerprint)) {
		return false
	}

	adopted, err := walkRevocationChain(ctx, h.services.crypto, peerServerID, pin.KeyID, pin.Armor, h.peerServerKeySource(peerServerID, pin.BaseURL))
	switch {
	case errors.Is(err, errKeyHandoverCompromised):
		log.Warn().Str("peerServerID", peerServerID).Msg("Peer reports its pinned key compromised; clearing the pin")
		if err := h.services.db.ClearPeerPin(ctx, peerServerID); err != nil {
			log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to clear peer pin")
		}
		h.logFederationEvent(peerServerID, federationLogError, "Peer key compromised; re-approve the peer with its new key")
		return false
	case err != nil:
		log.Warn().Err(err).Str("peerServerID", peerServerID).Msg("Could not walk the peer's key revocations")
		return false
	case len(adopted) == 0:
		return false
	}

	if err := h.services.db.RepinPeerKey(ctx, peerServerID, adopted, h.countersign); err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to re-pin peer key")
		return false
	}
	newKey := adopted[len(adopted)-1].ID
	log.Info().Str("peerServerID", peerServerID).Str("keyID", newKey).Msg("Updated the peer's signing key")
	h.logFederationEvent(peerServerID, federationLogInfo, "Updated the peer's signing key to "+newKey)
	return true
}

// peerServerKeySource reads a peer's key revocations and keys from its own
// /api/keys endpoints, signed with our key, which the peer has pinned.
func (h *Handlers) peerServerKeySource(peerServerID, baseURL string) serverKeySource {
	return serverKeySource{
		revocation: func(ctx context.Context, keyID string) (*serverKeyRevocation, error) {
			target := strings.TrimRight(baseURL, "/") + "/api/keys/" + keyID + "/revocation"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				return nil, err
			}
			if err := h.setPeerProxyAuthHeaders(req, ""); err != nil {
				return nil, err
			}
			resp, err := h.federationHTTPClient().Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return nil, nil
			}
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("fetch peer key revocation: status %d", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("read peer key revocation: %w", err)
			}
			_, wire, err := decodePeerKeyRevocation(body)
			if err != nil {
				return nil, fmt.Errorf("decode peer key revocation: %w", err)
			}
			if wire == nil || wire.ServerID != peerServerID {
				return nil, errKeyHandoverBroken
			}
			return &wire.serverKeyRevocation, nil
		},
		armor: func(ctx context.Context, keyID string) (string, error) {
			fingerprint, _, ok := parseIdentityID(identityID(keyID))
			if !ok {
				return "", errKeyHandoverBroken
			}
			return h.fetchPeerServerKeyArmor(ctx, baseURL, peerServerID, fingerprint)
		},
	}
}

// logFederationEvent writes a line to a peer's federation log, best effort.
func (h *Handlers) logFederationEvent(peerServerID, level, message string) {
	ctx := context.Background()
	if err := h.services.db.logFederationServer(ctx, peerServerID, level, message); err != nil {
		h.services.log.GetLogger(ctx).Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to write federation server log")
	}
}

// notifyPeersOfServerKey tells each connected peer that hasn't accepted the
// current key about it, once. The notice is signed with the new key, which
// is what makes the peer walk our revocations.
func (h *Handlers) notifyPeersOfServerKey() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := h.services.log.GetLogger(ctx)

	rotated, err := h.services.db.HasServerKeyRevocations(ctx)
	if err != nil || !rotated {
		return
	}
	current := string(canonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint))
	peers, err := h.services.db.PeersAwaitingKey(ctx, current)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list peers awaiting the server key")
		return
	}
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(peer PeerServer) {
			defer wg.Done()
			status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/server-key", nil, nil)
			if err != nil || status < 200 || status >= 300 {
				log.Warn().Err(err).Int("status", status).Str("peerServerID", peer.ID).Msg("Peer did not accept the new server key")
				return
			}
			if err := h.services.db.SetPeerKeyAck(ctx, peer.ID, current); err != nil {
				log.Error().Err(err).Str("peerServerID", peer.ID).Msg("Failed to record peer key ack")
			}
		}(peer)
	}
	wg.Wait()
}

// ///////////////////////////////////////// //
//   key-revocation: a user key was revoked    //
// ///////////////////////////////////////// //

// notifyPeersOfKeyRevocation sends keyID's revocation once to each peer that
// fetched it for its users. A peer that accepts it drops its allocation and
// tells its own users; one that doesn't is retried when it next boots.
func (h *Handlers) notifyPeersOfKeyRevocation(keyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := h.services.log.GetLogger(ctx)

	peerIDs, err := h.services.db.PublicKeyServerHolders(ctx, keyID)
	if err != nil {
		log.Error().Err(err).Str("keyID", keyID).Msg("Failed to list peers holding a revoked key")
		return
	}
	for _, peerID := range peerIDs {
		h.sendKeyRevocationToPeer(ctx, peerID, keyID)
	}
}

// sendOwedKeyRevocations sends peerID every revocation it is still owed.
func (h *Handlers) sendOwedKeyRevocations(peerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keyIDs, err := h.services.db.RevokedKeysOwedToServer(ctx, peerID)
	if err != nil {
		h.services.log.GetLogger(ctx).Error().Err(err).Str("peerServerID", peerID).Msg("Failed to list key revocations owed to peer")
		return
	}
	for _, keyID := range keyIDs {
		h.sendKeyRevocationToPeer(ctx, peerID, keyID)
	}
}

// notifyPeersOfOwedKeyRevocations retries, at boot, every revocation a
// connected peer hasn't accepted yet.
func (h *Handlers) notifyPeersOfOwedKeyRevocations() {
	peers, err := h.services.db.ListConnectedPeers(context.Background())
	if err != nil {
		h.services.log.GetLogger(context.Background()).Error().Err(err).Msg("Failed to list peers for owed key revocations")
		return
	}
	for _, peer := range peers {
		h.sendOwedKeyRevocations(peer.ID)
	}
}

func (h *Handlers) sendKeyRevocationToPeer(ctx context.Context, peerID, keyID string) {
	log := h.services.log.GetLogger(ctx)
	rev, err := h.services.db.GetKeyRevocation(ctx, keyID)
	if err != nil || rev == nil {
		log.Error().Err(err).Str("keyID", keyID).Msg("Failed to load key revocation for peer")
		return
	}
	peer, err := h.services.db.GetServerByID(ctx, peerID)
	if err != nil || peer == nil {
		return
	}
	status, err := h.callPeerRelayEndpoint(ctx, peerID, peer.BaseURL, "/api/federation/relay/key-revocation", pbKeyRevocation(rev), nil)
	if err != nil || status < 200 || status >= 300 {
		log.Warn().Err(err).Int("status", status).Str("peerServerID", peerID).Str("keyID", keyID).Msg("Peer did not accept key revocation")
		return
	}
	if err := h.services.db.DeletePublicKeyServerAllocation(ctx, peerID, keyID); err != nil {
		log.Error().Err(err).Str("peerServerID", peerID).Str("keyID", keyID).Msg("Failed to clear peer key allocation")
	}
}

// KeyRevocationFromPeer receives a revocation of one of the calling peer's
// users' keys. It is verified, marked on the local allocations of that key,
// and pushed to their holders; offline holders get it on catch-up.
func (h *Handlers) KeyRevocationFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var msg pb.KeyRevocationCert
	if err := readRequest(r, &msg); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "key-revocation", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	rev := keyRevocationFromPB(&msg)
	if err := h.verifyPeerKeyRevocation(r.Context(), peerServerID, rev); err != nil {
		log.Warn().Err(err).Str("peerServerID", peerServerID).Str("keyID", rev.ID).Msg("Rejected key revocation from peer")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "key-revocation", false)
		writeError(w, http.StatusBadRequest, "Key revocation failed verification")
		return
	}
	if err := h.services.db.MarkAllocatedKeyRevoked(r.Context(), rev.ID); err != nil {
		log.Error().Err(err).Str("keyID", rev.ID).Msg("Failed to mark allocated key revoked")
		internalServerError(w)
		return
	}
	if h.realtimeRelay != nil {
		h.realtimeRelay.HandleForeignKeyRevocation(rev)
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "key-revocation", true)
	w.WriteHeader(http.StatusNoContent)
}

// verifyPeerKeyRevocation checks that rev revokes a key of one of peerServerID's
// users, signed by that key and countersigned by the peer.
func (h *Handlers) verifyPeerKeyRevocation(ctx context.Context, peerServerID string, rev KeyRevocation) error {
	owner, keyServerID, _, ok := parseKeyFingerprint(identityID(rev.ID))
	if !ok || keyServerID != peerServerID || string(canonicalID(keyServerID, owner)) != rev.UserID {
		return fmt.Errorf("key %s is not a key of a user of the calling peer", rev.ID)
	}

	key, err := h.resolvePublicKey(ctx, rev.ID)
	if err != nil || key == nil {
		return fmt.Errorf("resolve revoked key: %v", err)
	}
	userPayload := buildUserRevocationPayload(rev.UserID, rev.ID, rev.Reason)
	if err := h.services.crypto.verifySignature(string(userPayload), rev.UserSignature.Armor, key.Armor); err != nil {
		return fmt.Errorf("user signature: %w", err)
	}

	return h.verifyPeerCountersignature(ctx, peerServerID, rev.ServerSignature, func(serverFP string) []byte {
		return buildServerRevocationPayload(
			rev.UserID, rev.ID, rev.Reason, peerServerID, serverFP,
			rev.UserSignature.Armor, rev.ServerSignature.SignedAt.UTC().Truncate(time.Second),
		)
	})
}

// verifyPeerThreadRecord checks that rec is a thread by a user of
// peerServerID, signed by their key and countersigned by the peer, and
// returns its parts in order.
func (h *Handlers) verifyPeerThreadRecord(ctx context.Context, peerServerID string, rec threadRecordWire) ([]string, error) {
	if rec.Type != identityTypeThread || rec.ServerID != peerServerID || len(rec.ReedIDs) == 0 || rec.ReedIDs[0] != rec.ThreadID {
		return nil, fmt.Errorf("malformed thread record")
	}
	parts := make([]createReedParams, len(rec.ReedIDs))
	for i, id := range rec.ReedIDs {
		parts[i] = createReedParams{ReedID: id, UserID: rec.UserID}
	}
	if err := validateThreadParts(rec.UserID, parts); err != nil {
		return nil, err
	}
	if _, userServerID, ok := parseIdentityID(identityID(rec.UserID)); !ok || userServerID != peerServerID {
		return nil, fmt.Errorf("thread author is not a user of the calling peer")
	}
	if owner, keyServerID, _, ok := parseKeyFingerprint(identityID(rec.UserSignature.ID)); !ok ||
		string(canonicalID(keyServerID, owner)) != rec.UserID {
		return nil, fmt.Errorf("thread signed by a key of another user")
	}

	key, err := h.resolvePublicKey(ctx, rec.UserSignature.ID)
	if err != nil || key == nil {
		return nil, fmt.Errorf("resolve author key: %v", err)
	}
	userPayload := buildThreadUserPayload(peerServerID, rec.ThreadID, rec.ReedIDs)
	if err := h.services.crypto.verifySignature(string(userPayload), rec.UserSignature.Armor, key.Armor); err != nil {
		return nil, fmt.Errorf("user signature: %w", err)
	}

	if err := h.verifyPeerCountersignature(ctx, peerServerID, rec.ServerSignature, func(serverFP string) []byte {
		return buildThreadServerPayload(
			peerServerID, rec.ThreadID, rec.UserSignature.ID, serverFP,
			rec.UserSignature.Armor, rec.ServerSignature.SignedAt.UTC().Truncate(time.Second),
		)
	}); err != nil {
		return nil, err
	}
	return rec.ReedIDs, nil
}

// ThreadRemovalFromPeer receives a peer's thread removal with its record,
// stores it once both verify, and tells this server's users.
func (h *Handlers) ThreadRemovalFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var msg pb.ThreadRemoval
	if err := readRequest(r, &msg); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "thread-removal", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	rm := threadRemovalFromPB(&msg)
	if err := h.verifyPeerThreadRemoval(r.Context(), peerServerID, rm); err != nil {
		log.Warn().Err(err).Str("threadID", rm.Cert.ThreadID).Str("peerServerID", peerServerID).Msg("Refused peer thread removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "thread-removal", false)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.services.db.InsertThreadRemoval(r.Context(), rm); err != nil && !errors.Is(err, errRemovalConflict) {
		log.Error().Err(err).Str("threadID", rm.Cert.ThreadID).Msg("Failed to store peer thread removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "thread-removal", false)
		internalServerError(w)
		return
	}
	for _, reedID := range rm.Record.ReedIDs {
		if err := h.dropForeignReedReferences(r.Context(), peerServerID, reedID, "", reedRemovalCert{}); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to drop references of removed thread part")
		}
	}
	if h.realtimeRelay != nil {
		h.realtimeRelay.HandleForeignThreadRemoval(&rm)
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "thread-removal", true)
	w.WriteHeader(http.StatusNoContent)
}

// verifyPeerThreadRemoval checks a peer's thread removal: its record must
// verify, and the certificate must be the author's, countersigned by the
// peer and bound to that record's signature.
func (h *Handlers) verifyPeerThreadRemoval(ctx context.Context, peerServerID string, rm threadRemoval) error {
	cert := rm.Cert
	if author, ok := authorOf(identityID(cert.ThreadID)); !ok || string(author) != rm.Record.UserID {
		return fmt.Errorf("thread_id does not belong to the record's author")
	}
	if _, serverID, ok := parseIdentityID(identityID(rm.Record.UserID)); !ok || serverID != peerServerID {
		return fmt.Errorf("thread_id does not belong to the calling peer")
	}
	if cert.Type != identityTypeThreadRemoval || cert.ThreadID != rm.Record.ThreadID || cert.UserID != rm.Record.UserID {
		return fmt.Errorf("certificate does not name the record's thread")
	}
	if _, err := h.verifyPeerThreadRecord(ctx, peerServerID, rm.Record); err != nil {
		return fmt.Errorf("thread record: %w", err)
	}
	if owner, keyServerID, _, ok := parseKeyFingerprint(identityID(cert.UserSignature.ID)); !ok ||
		string(canonicalID(keyServerID, owner)) != cert.UserID {
		return fmt.Errorf("removal signed by a key of another user")
	}
	key, err := h.resolvePublicKey(ctx, cert.UserSignature.ID)
	if err != nil || key == nil {
		return fmt.Errorf("resolve removal key: %v", err)
	}
	// Built from the record's signature: a certificate for another record fails here.
	userPayload := buildThreadRemovalUserPayload(peerServerID, cert.ThreadID, rm.Record.UserSignature.Armor)
	if err := h.services.crypto.verifySignature(string(userPayload), cert.UserSignature.Armor, key.Armor); err != nil {
		return fmt.Errorf("user signature: %w", err)
	}
	return h.verifyPeerCountersignature(ctx, peerServerID, cert.ServerSignature, func(serverFP string) []byte {
		return buildThreadRemovalServerPayload(
			peerServerID, cert.ThreadID, cert.UserSignature.ID, serverFP,
			cert.UserSignature.Armor, cert.ServerSignature.SignedAt.UTC().Truncate(time.Second),
		)
	})
}

// verifyPeerCountersignature checks that sig is peerServerID's, by its
// pinned key or one fetched from it by fingerprint, over payload(fingerprint).
func (h *Handlers) verifyPeerCountersignature(ctx context.Context, peerServerID string, sig ServerSignature, payload func(serverFP string) []byte) error {
	serverFP, sigServerID, ok := parseIdentityID(identityID(sig.ID))
	if !ok || sigServerID != peerServerID {
		return fmt.Errorf("countersignature is not the calling peer's")
	}
	pin, err := h.services.db.GetPeerPin(ctx, peerServerID)
	if err != nil || pin == nil {
		return fmt.Errorf("calling peer has no pinned key")
	}
	serverArmor := pin.Armor
	if pin.KeyID != sig.ID {
		if serverArmor, err = h.fetchPeerServerKeyArmor(ctx, pin.BaseURL, peerServerID, serverFP); err != nil {
			return fmt.Errorf("fetch countersigning key: %w", err)
		}
	}
	if err := h.services.crypto.verifySignature(string(payload(serverFP)), sig.Armor, serverArmor); err != nil {
		return fmt.Errorf("countersignature: %w", err)
	}
	return nil
}

// fetchForeignKeyRevocation fetches a foreign key's revocation from its home
// server, for delivery on catch-up; clients verify it themselves.
func (h *Handlers) fetchForeignKeyRevocation(ctx context.Context, keyID string) (*KeyRevocation, error) {
	_, homeServerID, _, ok := parseKeyFingerprint(identityID(keyID))
	if !ok {
		return nil, fmt.Errorf("malformed key id %s", keyID)
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil || peer == nil {
		return nil, fmt.Errorf("unknown home server %s", homeServerID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(peer.BaseURL, "/")+"/api/keys/"+keyID+"/revocation", nil)
	if err != nil {
		return nil, err
	}
	if err := h.setPeerProxyAuthHeaders(req, ""); err != nil {
		return nil, err
	}
	resp, err := h.federationHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch key revocation: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	rev, _, err := decodePeerKeyRevocation(body)
	if err != nil {
		return nil, err
	}
	if rev == nil {
		return nil, errors.New("not a user key revocation")
	}
	return rev, nil
}
