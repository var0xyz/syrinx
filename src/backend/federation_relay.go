//go:build !ops && !ripplescleanup

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"syrinx/observability/metrics"
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

// callPeerRelayEndpoint POSTs a JSON body to a peer's relay RPC path,
// signed as this server's own key, and decodes a JSON response into out
// (nil to ignore the body). Returns the peer's HTTP status. peerServerID
// is this server's own DB id for the peer (already resolved by the
// caller from the reed/author identity) — recorded on the outbound
// federation-relay metric so traffic can be broken down per peer.
func (h *Handlers) callPeerRelayEndpoint(ctx context.Context, peerServerID, baseURL, path string, body, out any) (status int, err error) {
	leg := relayLeg(path)
	ok := false
	defer func() { h.metrics.FederationRelay(ctx, metrics.DirectionOut, peerServerID, leg, ok) }()

	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	target := strings.TrimRight(baseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := h.setPeerProxyAuthHeaders(httpReq, string(payload)); err != nil {
		return 0, err
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	return resp.StatusCode, nil
}

// ///////////////////////////////////// //
//   Leg 1: register-request (O -> H)    //
// ///////////////////////////////////// //

type relayRequestPayload struct {
	ReedID          string `json:"reed_id"`
	AuthorID        string `json:"author_id"`
	RequesterUserID string `json:"requester_user_id"`
	PeerRequestID   string `json:"peer_request_id"`
}

type relayRequestResponse struct {
	PeerEventID string `json:"peer_event_id"`
	Status      string `json:"status"`
}

// relayRequestToPeer is HandleForeignRequestReed's hook implementation
// (leg 1, O's side): registers requesterUserID's interest in reedID with
// reedID's home server over peer HTTP.
func (h *Handlers) relayRequestToPeer(ctx context.Context, reedID, requesterUserID, localRequestID string) (realtimeForeignRequestResult, string, error) {
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

	payload := relayRequestPayload{
		ReedID:          bareReedID,
		AuthorID:        string(canonicalID(homeServerID, authorUserID)),
		RequesterUserID: requesterUserID,
		PeerRequestID:   localRequestID,
	}
	var respBody relayRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/request", payload, &respBody)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	switch {
	case status == http.StatusOK:
		return realtimeForeignRequestOK, respBody.PeerEventID, nil
	case status == http.StatusAccepted:
		return realtimeForeignRequestAccepted, respBody.PeerEventID, nil
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.AuthorID == "" || req.RequesterUserID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeResponse(w, http.StatusBadRequest, "reed_id, author_id, and requester_user_id are required")
		return
	}

	// Loop-prevention: this server can only ever be "home" for reeds it
	// actually authors locally — never chain a request further to a third
	// server. author_id's embedded serverID must be this server's own.
	authorUserID, embeddedServerID, parseOK := parseIdentityID(identityID(req.AuthorID))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeResponse(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestID, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		writeResponse(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}
	canonicalReedID := string(appendEntity(canonicalID(h.services.db.GetServerID(), authorUserID), req.ReedID))

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		internalServerError(w)
		return
	}
	result, peerEventID, err := h.realtimeRelay.HandleForeignRequestReed(r.Context(), canonicalReedID, peerServerID, req.RequesterUserID, req.PeerRequestID)
	if err != nil {
		log.Error().Err(err).Str("reedID", canonicalReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", false)
		internalServerError(w)
		return
	}
	switch result {
	case realtimeForeignRequestReedNotFound:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusNotFound, "Reed not found")
	case realtimeForeignRequestReedNotHeld:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusConflict, "Reed is not currently held")
	case realtimeForeignRequestAccepted:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusAccepted, relayRequestResponse{PeerEventID: peerEventID, Status: "accepted"})
	default:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "request", true)
		writeResponse(w, http.StatusOK, relayRequestResponse{PeerEventID: peerEventID, Status: "ack"})
	}
}

// ///////////////////////////////////// //
//   profile-page (O -> H)               //
// ///////////////////////////////////// //
//
// One page of a foreign author's reed ids. The asking server opens each
// one itself and folds it at the border.

type relayProfilePagePayload struct {
	AuthorID string `json:"author_id"`
	Page     int    `json:"page"`
}

type relayProfilePageResponse struct {
	ReedIDs []string `json:"reed_ids"`
	Count   int      `json:"count"`
	HasMore bool     `json:"has_more"`
}

// profilePageToPeer is ForeignProfilePageHook's implementation (O's side):
// asks authorID's home server for one page of their reeds. count/hasMore
// come straight from that server, since only it can see the whole list.
func (h *Handlers) profilePageToPeer(ctx context.Context, authorID string, page int) ([]string, int, bool, error) {
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

	payload := relayProfilePagePayload{AuthorID: authorID, Page: page}
	var respBody relayProfilePageResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/profile-page", payload, &respBody)
	if err != nil {
		return nil, 0, false, err
	}
	if status != http.StatusOK {
		return nil, 0, false, nil
	}

	return respBody.ReedIDs, respBody.Count, respBody.HasMore, nil
}

// RelayProfilePageFromPeer is the home-server handler for a history page:
// an established peer is asking for one page of a local author's reeds on
// behalf of one of its own users.
func (h *Handlers) RelayProfilePageFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayProfilePagePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	if req.AuthorID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeResponse(w, http.StatusBadRequest, "author_id is required")
		return
	}

	// Loop-prevention: this server can only ever be "home" for authors it
	// actually hosts locally.
	_, embeddedServerID, parseOK := parseIdentityID(identityID(req.AuthorID))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		writeResponse(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		internalServerError(w)
		return
	}
	reedIDs, count, hasMore, err := h.realtimeRelay.HandleForeignProfilePage(r.Context(), req.AuthorID, req.Page)
	if err != nil {
		log.Error().Err(err).Str("authorID", req.AuthorID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign profile page")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", false)
		internalServerError(w)
		return
	}

	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "profile-page", true)
	writeResponse(w, http.StatusOK, relayProfilePageResponse{ReedIDs: reedIDs, Count: count, HasMore: hasMore})
}

// ///////////////////////////////////// //
//   Leg 2: deliver-response (H -> O)    //
// ///////////////////////////////////// //

type relayDeliverPayload struct {
	PeerEventID string          `json:"peer_event_id"`
	Data        json.RawMessage `json:"data"`
}

// deliverRelayResponseToPeer is handleRelayResponse's foreignDeliverHook
// implementation (leg 2, H's side): delivers relayed content back to the
// requesting peer over HTTP instead of a (nonexistent) local WS connection.
func (h *Handlers) deliverRelayResponseToPeer(ctx context.Context, requestingServerID, peerEventID string, data json.RawMessage) error {
	peer, err := h.services.db.GetServerByID(ctx, requestingServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := relayDeliverPayload{PeerEventID: peerEventID, Data: data}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayDeliverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		internalServerError(w)
		return
	}
	found, err := h.realtimeRelay.HandleForeignRelayResponse(r.Context(), req.PeerEventID, peerServerID, req.Data)
	if err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign relay response")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", false)
		internalServerError(w)
		return
	}
	if !found {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", true)
		writeResponse(w, http.StatusNotFound, "Unknown or already-resolved event")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "deliver", true)
	writeResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ///////////////////////////////////////// //
//   Leg 2b: not-held / give-up (H -> O)     //
// ///////////////////////////////////////// //
//
// Failure counterpart of leg 2: H registered O's request (leg 1) but
// exhausted every holder before anyone could relay the content. Without
// this, O's local pending event would sit forever with nothing to notify
// its requester or clean up its own bookkeeping.

type relayNotHeldPayload struct {
	PeerEventID string `json:"peer_event_id"`
}

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
	payload := relayNotHeldPayload{PeerEventID: peerEventID}
	_, err = h.callPeerRelayEndpoint(ctx, requestingServerID, peer.BaseURL, "/api/federation/relay/not-held", payload, nil)
	return err
}

// RelayNotHeldFromPeer is leg 2b's originating-server handler: the home
// server is telling us it gave up on a request we registered via leg 1.
func (h *Handlers) RelayNotHeldFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayNotHeldPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		internalServerError(w)
		return
	}
	found, err := h.realtimeRelay.HandleForeignRelayNotHeld(r.Context(), req.PeerEventID, peerServerID)
	if err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign relay not-held")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", false)
		internalServerError(w)
		return
	}
	if !found {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", true)
		writeResponse(w, http.StatusNotFound, "Unknown or already-resolved event")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "not-held", true)
	writeResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ///////////////////////////////////// //
//   Leg 4: cancel-request (O -> H)      //
// ///////////////////////////////////// //

type relayCancelPayload struct {
	PeerEventID string `json:"peer_event_id"`
}

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
	payload := relayCancelPayload{PeerEventID: peerEventID}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayCancelPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.CancelForeignPendingEvent(r.Context(), req.PeerEventID, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to cancel foreign pending event")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "cancel", false)
		writeResponse(w, http.StatusForbidden, "Forbidden")
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

type relayAckPayload struct {
	PeerEventID string `json:"peer_event_id"`
}

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
	payload := relayAckPayload{PeerEventID: peerEventID}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayAckPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignAck(r.Context(), req.PeerEventID, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign ack")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "ack", false)
		writeResponse(w, http.StatusForbidden, "Forbidden")
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

type relaySubscribeReedPayload struct {
	ReedID          string `json:"reed_id"`
	RequesterUserID string `json:"requester_user_id"`
}

type relaySubscribeReedResponse struct {
	Found           bool `json:"found"`
	Echoes          int  `json:"echoes"`
	CoveragePercent int  `json:"coverage_percent"`
	Replies         int  `json:"replies"`
	Likes           int  `json:"likes"`
}

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

	payload := relaySubscribeReedPayload{ReedID: reedID, RequesterUserID: requesterUserID}
	var respBody relaySubscribeReedResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/subscribe-reed", payload, &respBody)
	if err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	if status != http.StatusOK || !respBody.Found {
		return realtimeForeignReedStatsSnapshot{}, false, nil
	}

	return realtimeForeignReedStatsSnapshot{
		Echoes:          respBody.Echoes,
		CoveragePercent: respBody.CoveragePercent,
		Replies:         respBody.Replies,
		Likes:           respBody.Likes,
	}, true, nil
}

// RelaySubscribeReedFromPeer is leg 8's home-server handler: an
// established peer's viewer wants live stats for one of this server's reeds.
func (h *Handlers) RelaySubscribeReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relaySubscribeReedPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	// Loop-prevention/spoof guard: this server can only be "home" for
	// reeds it hosts locally, and a peer may only register its own users.
	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedID))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := parseIdentityID(identityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		internalServerError(w)
		return
	}
	snapshot, found, err := h.realtimeRelay.HandleForeignSubscribeReed(r.Context(), req.ReedID, peerServerID, req.RequesterUserID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed stats subscription")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "subscribe-reed", true)
	writeResponse(w, http.StatusOK, relaySubscribeReedResponse{
		Found:           found,
		Echoes:          snapshot.Echoes,
		CoveragePercent: snapshot.CoveragePercent,
		Replies:         snapshot.Replies,
		Likes:           snapshot.Likes,
	})
}

// ///////////////////////////////////////// //
//   Leg 9: unsubscribe-reed (O -> H)        //
// ///////////////////////////////////////// //

type relayUnsubscribeReedPayload struct {
	ReedID          string `json:"reed_id"`
	RequesterUserID string `json:"requester_user_id"`
}

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
	payload := relayUnsubscribeReedPayload{ReedID: reedID, RequesterUserID: requesterUserID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/unsubscribe-reed", payload, nil)
	return err
}

// RelayUnsubscribeReedFromPeer is leg 9's home-server handler.
func (h *Handlers) RelayUnsubscribeReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayUnsubscribeReedPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedID))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := parseIdentityID(identityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unsubscribe-reed", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignUnsubscribeReed(r.Context(), req.ReedID, req.RequesterUserID); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("requesterUserID", req.RequesterUserID).Msg("Failed to handle foreign reed stats unsubscribe")
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

type relayReedStatsPayload struct {
	ReedID        string          `json:"reed_id"`
	ExcludeUserID string          `json:"exclude_user_id,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// pushReedStatsToPeer sends one live update for reedID to a peer, once for
// all of its viewers. The status tells the caller whether the peer still
// has anyone subscribed.
func (h *Handlers) pushReedStatsToPeer(ctx context.Context, peerServerID, reedID, excludeUserID string, payload json.RawMessage) (int, error) {
	peer, err := h.services.db.GetServerByID(ctx, peerServerID)
	if err != nil {
		return 0, err
	}
	if peer == nil {
		return http.StatusNotFound, nil
	}
	body := relayReedStatsPayload{ReedID: reedID, ExcludeUserID: excludeUserID, Payload: payload}
	return h.callPeerRelayEndpoint(ctx, peerServerID, peer.BaseURL, "/api/federation/relay/reed-stats", body, nil)
}

// PushReedStatsFromPeer forwards a live update for one of the peer's reeds
// to this server's users subscribed to it. 404 means nobody here is, so
// the peer can drop its subscriptions for us.
func (h *Handlers) PushReedStatsFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayReedStatsPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	if req.ReedID == "" || len(req.Payload) == 0 {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeResponse(w, http.StatusBadRequest, "reed_id and payload are required")
		return
	}
	// A peer only reports on its own reeds.
	_, reedServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedID))
	if !reedOK || reedServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeResponse(w, http.StatusBadRequest, "reed_id does not belong to the calling peer")
		return
	}
	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		internalServerError(w)
		return
	}

	delivered, err := h.realtimeRelay.DeliverForeignReedStats(r.Context(), req.ReedID, req.ExcludeUserID, req.Payload)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to deliver foreign reed stats push")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", false)
		writeResponse(w, http.StatusBadRequest, "Invalid payload")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-stats", true)
	if !delivered {
		writeResponse(w, http.StatusNotFound, "No subscribers for this reed")
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

type relayHolderNotifyPayload struct {
	ReedID string `json:"reed_id"`
}

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
	payload := relayHolderNotifyPayload{ReedID: reedID}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayHolderNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	if req.ReedID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		writeResponse(w, http.StatusBadRequest, "reed_id is required")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "holder-notify", false)
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleHolderNotify(r.Context(), req.ReedID, peerServerID); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle holder notify")
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

type relayFallbackRequestPayload struct {
	ReedID          string `json:"reed_id"`
	RequesterUserID string `json:"requester_user_id"`
	PeerRequestID   string `json:"peer_request_id"`
}

type relayFallbackRequestResponse struct {
	PeerEventID string `json:"peer_event_id"`
	Status      string `json:"status"`
}

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

	payload := relayFallbackRequestPayload{
		ReedID:          reedID,
		RequesterUserID: requesterUserID,
		PeerRequestID:   localRequestID,
	}
	var respBody relayFallbackRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, peerServerID, peer.BaseURL, "/api/federation/relay/fallback-request", payload, &respBody)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	switch {
	case status == http.StatusOK:
		return realtimeForeignRequestOK, respBody.PeerEventID, nil
	case status == http.StatusAccepted:
		return realtimeForeignRequestAccepted, respBody.PeerEventID, nil
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayFallbackRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	// Inverse of leg 1's loop-prevention: the CALLER must own reedID —
	// this stops any peer from asking us to hand back content on behalf
	// of a reed it doesn't actually author.
	_, embeddedServerID, _, parseOK := parseKeyFingerprint(identityID(req.ReedID))
	if !parseOK || embeddedServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeResponse(w, http.StatusBadRequest, "reed_id is not owned by the calling peer")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestID, peerServerID) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		writeResponse(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		internalServerError(w)
		return
	}
	result, peerEventID, err := h.realtimeRelay.HandleForeignFallbackRequest(r.Context(), req.ReedID, peerServerID, req.RequesterUserID, req.PeerRequestID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle fallback request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", false)
		internalServerError(w)
		return
	}
	switch result {
	case realtimeForeignRequestReedNotFound:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusNotFound, "Reed not found")
	case realtimeForeignRequestReedNotHeld:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusConflict, "Reed is not currently held")
	case realtimeForeignRequestAccepted:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusAccepted, relayFallbackRequestResponse{PeerEventID: peerEventID, Status: "accepted"})
	default:
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "fallback-request", true)
		writeResponse(w, http.StatusOK, relayFallbackRequestResponse{PeerEventID: peerEventID, Status: "ack"})
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

type relaySearchUsersPayload struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type relaySearchUsersResponse struct {
	Users []UserSearchResult `json:"users"`
}

// searchUsersFromPeer asks one peer to run its own local user search —
// the single-peer primitive fanoutUserSearchToPeers calls concurrently
// for every connected peer.
func (h *Handlers) searchUsersFromPeer(ctx context.Context, peer PeerServer, query string, limit int) ([]UserSearchResult, error) {
	payload := relaySearchUsersPayload{Query: query, Limit: limit}
	var respBody relaySearchUsersResponse
	status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/search-users", payload, &respBody)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("peer returned status %d", status)
	}
	return respBody.Users, nil
}

// fanoutUserSearchToPeers queries every connected peer in parallel, each
// bounded by perPeerTimeout, and returns the concatenation of whatever
// answered in time. A peer that errors or times out is silently dropped —
// this is a best-effort widening of local search results, not a
// completeness guarantee, so one flaky peer must never hold up or fail
// the request.
func (h *Handlers) fanoutUserSearchToPeers(ctx context.Context, query string, limit int, perPeerTimeout time.Duration) []UserSearchResult {
	peers, err := h.services.db.ListConnectedPeers(ctx)
	if err != nil || len(peers) == 0 {
		return nil
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
			peerResults, err := h.searchUsersFromPeer(peerCtx, peer, query, limit)
			if err != nil {
				return
			}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relaySearchUsersPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", true)
		writeResponse(w, http.StatusOK, relaySearchUsersResponse{Users: []UserSearchResult{}})
		return
	}

	// No self-exclusion here: the searching user is on the peer's own
	// server, never a local u.id on this one.
	results, err := h.services.db.SearchUsers(r.Context(), req.Query, "", req.Limit)
	if err != nil {
		log.Error().Err(err).Str("query", req.Query).Str("peerServerID", peerServerID).Msg("Failed to handle foreign search-users request")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", false)
		internalServerError(w)
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "search-users", true)
	writeResponse(w, http.StatusOK, relaySearchUsersResponse{Users: results})
}

type relayDisconnectNotifyPayload struct {
	Reason string `json:"reason"`
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
	payload := relayDisconnectNotifyPayload{Reason: reason}
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayDisconnectNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "disconnect-notify", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
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
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "disconnect-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   new-reed and reed-removal: to every peer   //
// ///////////////////////////////////// //

type relayNewReedReply struct {
	ParentReedID string `json:"parent_reed_id"`
	ThreadID     string `json:"thread_id"`
}

type relayNewReedEcho struct {
	EchoedReedID string `json:"echoed_reed_id"`
	IsBlank      bool   `json:"is_blank"`
}

// relayNewReedPayload announces one of the sender's own reeds. It carries
// no content: each recipient here fetches that through the fold.
type relayNewReedPayload struct {
	ReedID   string             `json:"reed_id"`
	AuthorID string             `json:"author_id"`
	SignedAt time.Time          `json:"signed_at"`
	Mentions []string           `json:"mentions,omitempty"`
	Reply    *relayNewReedReply `json:"reply,omitempty"`
	Echo     *relayNewReedEcho  `json:"echo,omitempty"`
}

// relayReedRemovalPayload is the signed removal cert, plus the parent the
// removed reed replied to so the receiver can reach that thread's viewers.
type relayReedRemovalPayload struct {
	relayReedRemovalCert
	ParentReedID string `json:"parent_reed_id,omitempty"`
}

// NewReedFromPeer handles a peer announcing a reed one of its users just
// posted. This server records what concerns it (mentions of its users,
// replies to and echoes of its reeds) and dispatches to its own users.
func (h *Handlers) NewReedFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fail := func(status int, msg string) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "new-reed", false)
		writeResponse(w, status, msg)
	}

	var req relayNewReedPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	authorBareID, reedServerID, _, ok := parseKeyFingerprint(identityID(req.ReedID))
	if !ok || reedServerID != peerServerID || string(canonicalID(reedServerID, authorBareID)) != req.AuthorID {
		fail(http.StatusBadRequest, "reed_id and author_id must belong to the calling peer")
		return
	}
	if req.Reply != nil && req.Echo != nil {
		fail(http.StatusBadRequest, "a reed is a reply or an echo, not both")
		return
	}
	if req.Reply != nil && (req.Reply.ParentReedID == "" || req.Reply.ThreadID == "") {
		fail(http.StatusBadRequest, "reply needs parent_reed_id and thread_id")
		return
	}
	if req.Echo != nil && req.Echo.EchoedReedID == "" {
		fail(http.StatusBadRequest, "echo needs echoed_reed_id")
		return
	}
	if h.realtimeRelay == nil {
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	if err := h.receiveForeignNewReed(r.Context(), peerServerID, req); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to receive foreign new reed")
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "new-reed", true)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) receiveForeignNewReed(ctx context.Context, peerServerID string, req relayNewReedPayload) error {
	db := h.services.db
	self := db.GetServerID()
	if err := db.UpsertRemoteIdentity(ctx, req.AuthorID, peerServerID); err != nil {
		return err
	}
	if err := db.UpsertReedIdentity(ctx, req.ReedID); err != nil {
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
		if err := db.InsertMentionRow(ctx, req.ReedID, string(canonicalID(serverID, bareID))); err != nil {
			return err
		}
	}

	foreignParentID := ""
	if req.Reply != nil {
		_, parentServerID, _, ok := parseKeyFingerprint(identityID(req.Reply.ParentReedID))
		switch {
		case !ok:
		case parentServerID == self:
			// Records the reply and notifies the thread's local viewers.
			if err := h.realtimeRelay.HandleForeignReplyNotify(ctx, req.Reply.ParentReedID, req.ReedID, req.Reply.ThreadID, req.SignedAt); err != nil {
				return err
			}
		default:
			foreignParentID = req.Reply.ParentReedID
		}
	}

	if req.Echo != nil {
		echoedAuthorBareID, echoedServerID, bareEchoedReedID, ok := parseKeyFingerprint(identityID(req.Echo.EchoedReedID))
		if ok && echoedServerID == self {
			echoedAuthorID := string(canonicalID(echoedServerID, echoedAuthorBareID))
			if err := db.InsertForeignEcho(ctx, req.ReedID, req.Echo.EchoedReedID, req.AuthorID, echoedAuthorID, req.Echo.IsBlank, req.SignedAt); err != nil {
				return err
			}
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
				UserID: echoedAuthorID,
				ReedID: bareEchoedReedID,
			}
		}
	}

	h.realtimeRelay.dispatchForeignNewReed(ctx, req.ReedID, req.AuthorID, foreignParentID)
	return nil
}

// ReedRemovalFromPeer handles a peer removing one of its users' reeds.
// Besides the removal itself it drops any reply or echo reference this
// server kept for the reed and tells the affected thread's viewers.
func (h *Handlers) ReedRemovalFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayReedRemovalPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	cert, status, msg := h.acceptForeignReedRemoval(r.Context(), peerServerID, &req.relayReedRemovalCert)
	if status != 0 {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		writeResponse(w, status, msg)
		return
	}
	if err := h.dropForeignReedReferences(r.Context(), peerServerID, req.ReedID, req.ParentReedID, cert); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Msg("Failed to drop references of removed foreign reed")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "reed-removal", false)
		internalServerError(w)
		return
	}
	if h.realtimeRelay != nil {
		wire := newReedRemovalWire(peerServerID, cert)
		h.realtimeRelay.HandleForeignReedRemoval(req.UserID, req.ReedID, &wire)
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

type relayRealtimeResetPayload struct {
	Reason string `json:"reason"`
}

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
	payload := relayRealtimeResetPayload{Reason: reason}
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(peer PeerServer) {
			defer wg.Done()
			status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/realtime-reset", payload, nil)
			if err == nil && (status < 200 || status >= 300) {
				err = fmt.Errorf("peer answered %d", status)
			}
			if err != nil {
				log.Warn().Err(err).Str("peerServerID", peer.ID).Str("reason", reason).Msg("Failed to send realtime reset to peer")
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayRealtimeResetPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Reason != realtimeResetShutdown && req.Reason != realtimeResetBoot {
		log.Error().Str("peerServerID", peerServerID).Str("reason", req.Reason).Msg("realtime reset with unknown reason")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		writeResponse(w, http.StatusBadRequest, "`reason` must be shutdown or boot")
		return
	}
	if h.realtimeRelay == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", false)
		internalServerError(w)
		return
	}

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
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "realtime-reset", true)
	w.WriteHeader(http.StatusNoContent)
}

// account-removal-notify: a removed user's own home server notifies every
// peer known to hold a copy of that user's content — not followers or
// subscribers, which are a peer-local concern resolved once it has the cert.

type relayAccountRemovalNotifyPayload struct {
	UserID            string    `json:"user_id"`
	Note              string    `json:"note"`
	UserSignature     string    `json:"user_signature"`
	UserKeyID         string    `json:"user_key_id"`
	ServerSignature   string    `json:"server_signature"`
	ServerFingerprint string    `json:"server_fingerprint"`
	ServerSignedAt    time.Time `json:"server_signed_at"`
}

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
	payload := relayAccountRemovalNotifyPayload{
		UserID:            removedUserID,
		Note:              cert.Note,
		UserSignature:     cert.UserSignature,
		UserKeyID:         cert.UserKeyID,
		ServerSignature:   cert.ServerSignature,
		ServerFingerprint: cert.ServerFingerprint,
		ServerSignedAt:    cert.ServerSignedAt,
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
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayAccountRemovalNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" || req.UserSignature == "" || req.ServerSignature == "" {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "user_id, user_signature, and server_signature are required")
		return
	}

	// Loop-prevention: a peer may only notify us about removal of one of
	// its OWN users — never claim removal on behalf of a third server.
	_, userServerID, userOK := parseIdentityID(identityID(req.UserID))
	if !userOK || userServerID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "user_id does not belong to the calling peer")
		return
	}

	if err := h.services.db.UpsertRemoteIdentity(r.Context(), req.UserID, peerServerID); err != nil {
		log.Error().Err(err).Str("userID", req.UserID).Msg("Failed to upsert remote identity for account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}

	// account_removals.public_key_id is a hard FK — a foreign user's key
	// is fetched and cached from its owning peer if we don't hold it yet
	// (same resolvePublicKey path LikeReed uses for the same problem).
	pubKey, err := h.resolvePublicKey(r.Context(), req.UserKeyID)
	if err != nil {
		log.Error().Err(err).Str("userID", req.UserID).Str("userKeyID", req.UserKeyID).Msg("Failed to resolve signing key for account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}
	if pubKey == nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "user_key_id could not be resolved")
		return
	}

	// The author signs against their own home server's id, which for a peer
	// notification is the calling peer.
	userSigArmor, err := base64Decode(req.UserSignature)
	if err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	userPayload := buildAccountRemovalUserPayload(peerServerID, req.UserID, req.Note)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Err(err).Str("userID", req.UserID).Str("peerServerID", peerServerID).Msg("Account-removal user signature verification failed")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		writeResponse(w, http.StatusBadRequest, "user_signature verification failed")
		return
	}

	cert := accountRemovalCert{
		UserID:            req.UserID,
		Note:              req.Note,
		UserSignature:     req.UserSignature,
		UserKeyID:         req.UserKeyID,
		ServerSignature:   req.ServerSignature,
		ServerFingerprint: req.ServerFingerprint,
		ServerSignedAt:    req.ServerSignedAt,
	}
	if err := h.services.db.InsertForeignAccountRemoval(r.Context(), cert); err != nil && !errors.Is(err, errRemovalConflict) {
		log.Error().Err(err).Str("userID", req.UserID).Str("peerServerID", peerServerID).Msg("Failed to store foreign account removal")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", false)
		internalServerError(w)
		return
	}

	if h.realtimeRelay != nil {
		wire := newAccountRemovalWire(peerServerID, cert)
		h.realtimeRelay.HandleForeignAccountRemoval(req.UserID, &wire)
	}

	log.Info().Str("userID", req.UserID).Str("peerServerID", peerServerID).Msg("Stored foreign account removal")
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "account-removal-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// relayReedRemovalCert is a signed reed removal as it travels between
// servers, inside reed-removal.

type relayReedRemovalCert struct {
	ReedID            string    `json:"reed_id"`
	UserID            string    `json:"user_id"`
	UserSignature     string    `json:"user_signature"`
	UserKeyID         string    `json:"user_key_id"`
	ServerSignature   string    `json:"server_signature"`
	ServerFingerprint string    `json:"server_fingerprint"`
	ServerSignedAt    time.Time `json:"server_signed_at"`
}

// acceptForeignReedRemoval verifies a peer's reed removal and stores the
// cert. A non-zero status is the HTTP answer for a rejected removal.
func (h *Handlers) acceptForeignReedRemoval(ctx context.Context, peerServerID string, req *relayReedRemovalCert) (reedRemovalCert, int, string) {
	log := h.services.log.GetLogger(ctx)
	req.ReedID = strings.TrimSpace(req.ReedID)
	if req.ReedID == "" || req.UserID == "" || req.UserSignature == "" || req.ServerSignature == "" {
		return reedRemovalCert{}, http.StatusBadRequest, "reed_id, user_id, user_signature, and server_signature are required"
	}

	// Loop-prevention: a peer may only notify us about removal of a reed
	// authored by one of its OWN users — never claim removal on behalf of
	// a third server.
	_, authorServerID, _, reedOK := parseKeyFingerprint(identityID(req.ReedID))
	if !reedOK || authorServerID != peerServerID {
		return reedRemovalCert{}, http.StatusBadRequest, "reed_id does not belong to the calling peer"
	}

	pubKey, err := h.resolvePublicKey(ctx, req.UserKeyID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("userKeyID", req.UserKeyID).Msg("Failed to resolve signing key for reed removal")
		return reedRemovalCert{}, http.StatusInternalServerError, "Internal Server Error"
	}
	if pubKey == nil {
		return reedRemovalCert{}, http.StatusBadRequest, "user_key_id could not be resolved"
	}

	// The author signs against their own home server's id, which for a peer
	// notification is the calling peer.
	userSigArmor, err := base64Decode(req.UserSignature)
	if err != nil {
		return reedRemovalCert{}, http.StatusBadRequest, "Invalid signature encoding"
	}
	userPayload := buildReedRemovalUserPayload(peerServerID, req.ReedID)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Reed-removal user signature verification failed")
		return reedRemovalCert{}, http.StatusBadRequest, "user_signature verification failed"
	}

	cert := reedRemovalCert{
		ReedID:            req.ReedID,
		UserID:            req.UserID,
		UserSignature:     req.UserSignature,
		UserKeyID:         req.UserKeyID,
		ServerSignature:   req.ServerSignature,
		ServerFingerprint: req.ServerFingerprint,
		ServerSignedAt:    req.ServerSignedAt,
	}
	if err := h.services.db.InsertReedRemoval(ctx, cert); err != nil && !errors.Is(err, errRemovalConflict) {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to store foreign reed removal")
		return reedRemovalCert{}, http.StatusInternalServerError, "Internal Server Error"
	}
	return cert, 0, ""
}
