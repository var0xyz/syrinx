//go:build !ops && !ripplescleanup

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"syrinx/identity"
	"syrinx/realtime"
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
	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(id))
	if !ok {
		return false
	}
	return embeddedServerID == callerServerID
}

// callPeerRelayEndpoint POSTs a JSON body to a peer's relay RPC path,
// signed as this server's own key, and decodes a JSON response into out
// (nil to ignore the body). Returns the peer's HTTP status.
func (h *Handlers) callPeerRelayEndpoint(ctx context.Context, baseURL, path string, body, out any) (status int, err error) {
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
func (h *Handlers) relayRequestToPeer(ctx context.Context, reedID, requesterUserID, localRequestID string) (realtime.ForeignRequestResult, string, error) {
	authorUserID, homeServerID, bareReedID, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		return realtime.ForeignRequestReedNotFound, "", nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return realtime.ForeignRequestReedNotFound, "", err
	}
	if peer == nil {
		return realtime.ForeignRequestReedNotFound, "", nil
	}

	payload := relayRequestPayload{
		ReedID:          bareReedID,
		AuthorID:        string(identity.CanonicalID(homeServerID, authorUserID)),
		RequesterUserID: requesterUserID,
		PeerRequestID:   localRequestID,
	}
	var respBody relayRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, peer.BaseURL, "/api/federation/relay/request", payload, &respBody)
	if err != nil {
		return realtime.ForeignRequestReedNotFound, "", err
	}
	switch {
	case status == http.StatusOK:
		return realtime.ForeignRequestOK, respBody.PeerEventID, nil
	case status == http.StatusNotFound:
		return realtime.ForeignRequestReedNotFound, "", nil
	case status == http.StatusConflict:
		return realtime.ForeignRequestReedNotHeld, "", nil
	default:
		return realtime.ForeignRequestReedNotFound, "", nil
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.AuthorID == "" || req.RequesterUserID == "" {
		writeResponse(w, http.StatusBadRequest, "reed_id, author_id, and requester_user_id are required")
		return
	}

	// Loop-prevention: this server can only ever be "home" for reeds it
	// actually authors locally — never chain a request further to a third
	// server. author_id's embedded serverID must be this server's own.
	authorUserID, embeddedServerID, parseOK := identity.ParseIdentityID(identity.IdentityID(req.AuthorID))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestID, peerServerID) {
		writeResponse(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}
	canonicalReedID := string(identity.AppendEntity(identity.CanonicalID(h.services.db.GetServerID(), authorUserID), req.ReedID))

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	result, peerEventID, err := h.realtimeRelay.HandleForeignRequestReed(r.Context(), canonicalReedID, peerServerID, req.RequesterUserID, req.PeerRequestID)
	if err != nil {
		log.Error().Err(err).Str("reedID", canonicalReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed request")
		internalServerError(w)
		return
	}
	switch result {
	case realtime.ForeignRequestReedNotFound:
		writeResponse(w, http.StatusNotFound, "Reed not found")
	case realtime.ForeignRequestReedNotHeld:
		writeResponse(w, http.StatusConflict, "Reed is not currently held")
	default:
		writeResponse(w, http.StatusOK, relayRequestResponse{PeerEventID: peerEventID, Status: "ack"})
	}
}

// //////////////////////////////////////////////////// //
//   Leg 1b: register-profile-subscription (O -> H)     //
// //////////////////////////////////////////////////// //
//
// Profile-level sibling of leg 1: a viewer's profile page needs every one
// of a foreign author's unallocated reeds, not just one. H enumerates its
// own author's reeds (a query only H can answer) and registers each one
// exactly like an individual leg-1 request, returning the full list of
// peer_event_ids in one response — each is independently deliverable via
// the existing /relay/deliver (leg 2) and /relay/cancel (leg 4), which
// are already generic over a single peer_event_id and need no changes.

type relaySubscribePayload struct {
	AuthorID        string `json:"author_id"`
	RequesterUserID string `json:"requester_user_id"`
}

type relaySubscribeResponseItem struct {
	PeerEventID string `json:"peer_event_id"`
}

type relaySubscribeResponse struct {
	Events []relaySubscribeResponseItem `json:"events"`
}

// subscribeProfileToPeer is ForeignSubscribeProfileHook's implementation
// (leg 1b, O's side): registers requesterUserID's interest in every one
// of authorID's (foreign) reeds with authorID's home server over peer HTTP.
func (h *Handlers) subscribeProfileToPeer(ctx context.Context, authorID, requesterUserID string) ([]realtime.ForeignSubscribeProfileResult, error) {
	_, homeServerID, ok := identity.ParseIdentityID(identity.IdentityID(authorID))
	if !ok {
		return nil, nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return nil, nil
	}

	payload := relaySubscribePayload{AuthorID: authorID, RequesterUserID: requesterUserID}
	var respBody relaySubscribeResponse
	status, err := h.callPeerRelayEndpoint(ctx, peer.BaseURL, "/api/federation/relay/subscribe", payload, &respBody)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, nil
	}

	results := make([]realtime.ForeignSubscribeProfileResult, 0, len(respBody.Events))
	for _, ev := range respBody.Events {
		results = append(results, realtime.ForeignSubscribeProfileResult{PeerEventID: ev.PeerEventID})
	}
	return results, nil
}

// RelaySubscribeProfileFromPeer is leg 1b's home-server handler: an
// established peer is registering a whole-profile backfill on behalf of
// one of its own users for one of THIS server's authors.
func (h *Handlers) RelaySubscribeProfileFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relaySubscribePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.AuthorID == "" || req.RequesterUserID == "" {
		writeResponse(w, http.StatusBadRequest, "author_id and requester_user_id are required")
		return
	}

	// Loop-prevention: identical guard to leg 1 — this server can only
	// ever be "home" for authors it actually hosts locally.
	_, embeddedServerID, parseOK := identity.ParseIdentityID(identity.IdentityID(req.AuthorID))
	if !parseOK || embeddedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	results, err := h.realtimeRelay.HandleForeignSubscribeProfile(r.Context(), req.AuthorID, peerServerID, req.RequesterUserID)
	if err != nil {
		log.Error().Err(err).Str("authorID", req.AuthorID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign profile subscription")
		internalServerError(w)
		return
	}

	resp := relaySubscribeResponse{Events: make([]relaySubscribeResponseItem, 0, len(results))}
	for _, r := range results {
		resp.Events = append(resp.Events, relaySubscribeResponseItem{PeerEventID: r.PeerEventID})
	}
	writeResponse(w, http.StatusOK, resp)
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
	_, err = h.callPeerRelayEndpoint(ctx, peer.BaseURL, "/api/federation/relay/deliver", payload, nil)
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	found, err := h.realtimeRelay.HandleForeignRelayResponse(r.Context(), req.PeerEventID, peerServerID, req.Data)
	if err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign relay response")
		internalServerError(w)
		return
	}
	if !found {
		writeResponse(w, http.StatusNotFound, "Unknown or already-resolved event")
		return
	}
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
	_, err = h.callPeerRelayEndpoint(ctx, peer.BaseURL, "/api/federation/relay/cancel", payload, nil)
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.CancelForeignPendingEvent(r.Context(), req.PeerEventID, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to cancel foreign pending event")
		writeResponse(w, http.StatusForbidden, "Forbidden")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////// //
//   Leg 5: ack-delivered (O -> H)       //
// ///////////////////////////////////// //
//
// Closes the loop left open by legs 1-4: once O's viewer verifies
// delivered content and O persists its own local allocation, O tells H
// so H can mirror that allocation against its per-peer sentinel. Without
// this, H has no way to know a relay actually landed, so every future
// profile-subscribe backfill re-offers content the peer already holds —
// this is what makes SUBSCRIBE_PROFILE's cross-server bridge behave like
// the local case (skip what's already held) instead of resending
// everything on every visit.

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
	_, err = h.callPeerRelayEndpoint(ctx, peer.BaseURL, "/api/federation/relay/ack", payload, nil)
	return err
}

// AckRelayDeliveryFromPeer is leg 5's home-server handler: the
// originating server's local viewer verified and allocated the delivered
// content; mirror that allocation against this peer's sentinel identity
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.PeerEventID == "" {
		writeResponse(w, http.StatusBadRequest, "peer_event_id is required")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignAck(r.Context(), req.PeerEventID, peerServerID); err != nil {
		log.Error().Err(err).Str("peerEventID", req.PeerEventID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign ack")
		writeResponse(w, http.StatusForbidden, "Forbidden")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
