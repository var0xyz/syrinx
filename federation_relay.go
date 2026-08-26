//go:build !ops && !ripplescleanup

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"syrinx/identity"
	"syrinx/observability/metrics"
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

// withFederationRelayMetric wraps one of the RelayFromPeer handlers below
// to record the inbound side of syrinx.federation.relay — every registration
// in main.go for a /federation/relay/... route goes through this rather
// than each of the 16 handlers recording it individually. leg matches
// relayLeg's derivation from the matching outbound call's path, e.g.
// "request" for /api/federation/relay/request. peerServerID comes from
// authenticateAsPeer's context value; a request that never got that far
// (auth middleware rejected it before reaching here) isn't counted — it's
// not federation traffic this server actually processed.
func (h *Handlers) withFederationRelayMetric(leg string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w}
		next(rw, r)
		peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
		if !ok || peerServerID == "" {
			return
		}
		status := rw.statusCode
		if status == 0 {
			status = http.StatusOK
		}
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, leg, status >= 200 && status < 300)
	}
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
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/request", payload, &respBody)
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
	ReedID      string `json:"reed_id"`
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
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/subscribe", payload, &respBody)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, nil
	}

	results := make([]realtime.ForeignSubscribeProfileResult, 0, len(respBody.Events))
	for _, ev := range respBody.Events {
		results = append(results, realtime.ForeignSubscribeProfileResult{PeerEventID: ev.PeerEventID, ReedID: ev.ReedID})
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
		resp.Events = append(resp.Events, relaySubscribeResponseItem{PeerEventID: r.PeerEventID, ReedID: r.ReedID})
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

// ///////////////////////////////////////// //
//   Leg 7: unsubscribe-profile (O -> H)     //
// ///////////////////////////////////////// //
//
// Teardown counterpart of leg 1b: without this, UNSUBSCRIBE_PROFILE only
// ever updated O's own bookkeeping, so H kept fanning out to a departed
// viewer's sentinel-attributed pending events forever.

type relayUnsubscribePayload struct {
	AuthorID        string `json:"author_id"`
	RequesterUserID string `json:"requester_user_id"`
}

// unsubscribeProfileWithPeer is the foreignUnsubscribeProfileHook
// implementation (leg 7, O's side): tells authorID's home server that
// requesterUserID no longer wants live fanout.
func (h *Handlers) unsubscribeProfileWithPeer(ctx context.Context, authorID, requesterUserID string) error {
	_, homeServerID, ok := identity.ParseIdentityID(identity.IdentityID(authorID))
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
	payload := relayUnsubscribePayload{AuthorID: authorID, RequesterUserID: requesterUserID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/unsubscribe", payload, nil)
	return err
}

// RelayUnsubscribeProfileFromPeer is leg 7's home-server handler: an
// established peer's viewer no longer wants live fanout for one of this
// server's authors.
func (h *Handlers) RelayUnsubscribeProfileFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayUnsubscribePayload
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

	// Loop-prevention/spoof guard: this server can only ever be "home" for
	// authors it hosts locally, and a peer may only unsubscribe its own
	// users — never claim to act on behalf of a third server's user.
	_, authorServerID, authorOK := identity.ParseIdentityID(identity.IdentityID(req.AuthorID))
	if !authorOK || authorServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "author_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := identity.ParseIdentityID(identity.IdentityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignUnsubscribeProfile(r.Context(), req.AuthorID, req.RequesterUserID); err != nil {
		log.Error().Err(err).Str("authorID", req.AuthorID).Str("requesterUserID", req.RequesterUserID).Msg("Failed to handle foreign profile unsubscribe")
		internalServerError(w)
		return
	}
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
func (h *Handlers) subscribeReedToPeer(ctx context.Context, reedID, requesterUserID string) (realtime.ForeignReedStatsSnapshot, bool, error) {
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		return realtime.ForeignReedStatsSnapshot{}, false, nil
	}
	peer, err := h.services.db.GetServerByID(ctx, homeServerID)
	if err != nil {
		return realtime.ForeignReedStatsSnapshot{}, false, err
	}
	if peer == nil {
		return realtime.ForeignReedStatsSnapshot{}, false, nil
	}

	payload := relaySubscribeReedPayload{ReedID: reedID, RequesterUserID: requesterUserID}
	var respBody relaySubscribeReedResponse
	status, err := h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/subscribe-reed", payload, &respBody)
	if err != nil {
		return realtime.ForeignReedStatsSnapshot{}, false, err
	}
	if status != http.StatusOK || !respBody.Found {
		return realtime.ForeignReedStatsSnapshot{}, false, nil
	}

	return realtime.ForeignReedStatsSnapshot{
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	// Loop-prevention/spoof guard: this server can only be "home" for
	// reeds it hosts locally, and a peer may only register its own users.
	_, reedServerID, _, reedOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ReedID))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := identity.ParseIdentityID(identity.IdentityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	snapshot, found, err := h.realtimeRelay.HandleForeignSubscribeReed(r.Context(), req.ReedID, peerServerID, req.RequesterUserID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reed stats subscription")
		internalServerError(w)
		return
	}
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
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	_, reedServerID, _, reedOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ReedID))
	if !reedOK || reedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "reed_id is not local to this server")
		return
	}
	_, requesterServerID, requesterOK := identity.ParseIdentityID(identity.IdentityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignUnsubscribeReed(r.Context(), req.ReedID, req.RequesterUserID); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("requesterUserID", req.RequesterUserID).Msg("Failed to handle foreign reed stats unsubscribe")
		internalServerError(w)
		return
	}
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
	RequesterUserID string          `json:"requester_user_id"`
	Payload         json.RawMessage `json:"payload"`
}

// pushReedStatsToPeer is the ForeignReedStatsHook implementation (leg 10,
// H's side): pushes payload to requestingServerID for delivery to requestingUserID.
func (h *Handlers) pushReedStatsToPeer(ctx context.Context, requestingServerID, requestingUserID string, payload json.RawMessage) error {
	peer, err := h.services.db.GetServerByID(ctx, requestingServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	body := relayReedStatsPayload{RequesterUserID: requestingUserID, Payload: payload}
	_, err = h.callPeerRelayEndpoint(ctx, requestingServerID, peer.BaseURL, "/api/federation/relay/reed-stats", body, nil)
	return err
}

// PushReedStatsFromPeer is leg 10's originating-server handler: a peer is
// delivering a live reed-stats update for one of its viewers, registered
// earlier via leg 8.
func (h *Handlers) PushReedStatsFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayReedStatsPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.RequesterUserID == "" || len(req.Payload) == 0 {
		writeResponse(w, http.StatusBadRequest, "requester_user_id and payload are required")
		return
	}

	// The viewer this push claims to be for must actually be one of this
	// peer's own users — a peer must not be able to push content to a
	// third server's user by spoofing requester_user_id.
	_, requesterServerID, requesterOK := identity.ParseIdentityID(identity.IdentityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "requester_user_id is not local to this server")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.DeliverForeignReedStats(r.Context(), req.RequesterUserID, req.Payload); err != nil {
		log.Error().Err(err).Str("requesterUserID", req.RequesterUserID).Str("peerServerID", peerServerID).Msg("Failed to deliver foreign reed stats push")
	}
	writeResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// /////////////////////////////////// //
//   Leg 11: reply-notify (O -> H)     //
// /////////////////////////////////// //
//
// Write side of cross-server replying: a reply to a foreign reed is
// created entirely on O (the replier's own server) — H never receives or
// stores its content. Without this leg H has no way to know the reply
// exists at all, so every reply-count/thread query it answers comes up
// empty even though the reply itself was created successfully.

type relayReplyNotifyPayload struct {
	ParentReedID string    `json:"parent_reed_id"`
	ReplyReedID  string    `json:"reply_reed_id"`
	ThreadID     string    `json:"thread_id"`
	Timestamp    time.Time `json:"timestamp"`
}

// notifyForeignReplyToPeer is the ForeignReplyNotifyHook implementation
// (leg 11, O's side): tells parentReedID's home server that replyReedID
// (authored here) replies to it.
func (h *Handlers) notifyForeignReplyToPeer(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error {
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(parentReedID))
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
	payload := relayReplyNotifyPayload{
		ParentReedID: parentReedID,
		ReplyReedID:  replyReedID,
		ThreadID:     threadID,
		Timestamp:    ts,
	}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/reply-notify", payload, nil)
	return err
}

// ReplyNotifyFromPeer is leg 11's home-server handler: an established
// peer is telling us one of its reeds replies to one of ours.
func (h *Handlers) ReplyNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayReplyNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ParentReedID = strings.TrimSpace(req.ParentReedID)
	req.ReplyReedID = strings.TrimSpace(req.ReplyReedID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	if req.ParentReedID == "" || req.ReplyReedID == "" || req.ThreadID == "" {
		writeResponse(w, http.StatusBadRequest, "parent_reed_id, reply_reed_id, and thread_id are required")
		return
	}

	// Loop-prevention: this server can only be "home" for reeds it hosts
	// locally, and a peer may only notify us of replies IT actually
	// authors — never claim a reply on behalf of a third server.
	_, parentServerID, _, parentOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ParentReedID))
	if !parentOK || parentServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "parent_reed_id is not local to this server")
		return
	}
	_, replyServerID, _, replyOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ReplyReedID))
	if !replyOK || replyServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "reply_reed_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleForeignReplyNotify(r.Context(), req.ParentReedID, req.ReplyReedID, req.ThreadID, req.Timestamp); err != nil {
		log.Error().Err(err).Str("parentReedID", req.ParentReedID).Str("replyReedID", req.ReplyReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reply notify")
		internalServerError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// //////////////////////////////////// //
//   Leg 12: echo-notify (O -> H)       //
// //////////////////////////////////// //
//
// Write side of cross-server echoing: an echo of a foreign reed is
// created entirely on O (the echoer's own server) — H never receives or
// stores its content. Without this leg H has no way to know the echo
// exists at all, so every echo-count/chorus query it answers comes up
// empty even though the echo itself was created successfully. Unlike a
// reply, an echo carries no content-relay dependency on the echoer's own
// PUBLISH_READY, so this fires immediately at SignReed time, same as the
// existing local EchoCountChanged broadcast.

type relayEchoNotifyPayload struct {
	EchoedReedID    string    `json:"echoed_reed_id"`
	EchoingReedID   string    `json:"echoing_reed_id"`
	EchoingAuthorID string    `json:"echoing_author_id"`
	IsBlank         bool      `json:"is_blank"`
	Timestamp       time.Time `json:"timestamp"`
}

// notifyForeignEchoToPeer tells echoedReedID's home server that
// echoingReedID (authored here, by echoingAuthorID) echoes it.
func (h *Handlers) notifyForeignEchoToPeer(ctx context.Context, echoedReedID, echoingReedID, echoingAuthorID string, isBlank bool, ts time.Time) error {
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(echoedReedID))
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
	payload := relayEchoNotifyPayload{
		EchoedReedID:    echoedReedID,
		EchoingReedID:   echoingReedID,
		EchoingAuthorID: echoingAuthorID,
		IsBlank:         isBlank,
		Timestamp:       ts,
	}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/echo-notify", payload, nil)
	return err
}

// EchoNotifyFromPeer is leg 12's home-server handler: an established peer
// is telling us one of its reeds echoes one of ours.
func (h *Handlers) EchoNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayEchoNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.EchoedReedID = strings.TrimSpace(req.EchoedReedID)
	req.EchoingReedID = strings.TrimSpace(req.EchoingReedID)
	req.EchoingAuthorID = strings.TrimSpace(req.EchoingAuthorID)
	if req.EchoedReedID == "" || req.EchoingReedID == "" || req.EchoingAuthorID == "" {
		writeResponse(w, http.StatusBadRequest, "echoed_reed_id, echoing_reed_id, and echoing_author_id are required")
		return
	}

	// Loop-prevention: this server can only be "home" for reeds it hosts
	// locally, and a peer may only notify us of echoes IT actually
	// authors — never claim an echo on behalf of a third server.
	_, echoedServerID, _, echoedOK := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoedReedID))
	if !echoedOK || echoedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "echoed_reed_id is not local to this server")
		return
	}
	_, echoingServerID, _, echoingOK := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoingReedID))
	if !echoingOK || echoingServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "echoing_reed_id does not belong to the calling peer")
		return
	}
	_, echoingAuthorServerID, echoingAuthorOK := identity.ParseIdentityID(identity.IdentityID(req.EchoingAuthorID))
	if !echoingAuthorOK || echoingAuthorServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "echoing_author_id does not belong to the calling peer")
		return
	}

	// echoedAuthorID/bareEchoedReedID: this reed is local, so its author
	// and bare id are recoverable directly from its own canonical id — H
	// doesn't need O to assert either.
	echoedAuthorBareID, _, bareEchoedReedID, _ := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoedReedID))
	echoedAuthorID := string(identity.CanonicalID(echoedServerID, echoedAuthorBareID))

	if err := h.services.db.InsertForeignEcho(r.Context(), req.EchoingReedID, req.EchoedReedID, req.EchoingAuthorID, echoedAuthorID, req.IsBlank, req.Timestamp); err != nil {
		log.Error().Err(err).Str("echoedReedID", req.EchoedReedID).Str("echoingReedID", req.EchoingReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign echo notify")
		internalServerError(w)
		return
	}

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.EchoCountChanged,
		UserID: echoedAuthorID,
		ReedID: bareEchoedReedID,
	}

	w.WriteHeader(http.StatusNoContent)
}

// //////////////////////////////////////// //
//   Leg 18: mention-notify (O -> H)         //
// //////////////////////////////////////// //
//
// Write side of cross-server mentions: a mention of a foreign user is
// extracted and recorded entirely on O (the mentioning reed's own
// server) — H never receives or stores the reed's content, only the
// fact that a mention happened. Like echo (and unlike reply), a mention
// carries no content-relay dependency on the mentioning author's own
// PUBLISH_READY, so this fires immediately at SignReed time.

type relayMentionNotifyPayload struct {
	MentioningReedID string    `json:"mentioning_reed_id"`
	MentionedUserID  string    `json:"mentioned_user_id"`
	Timestamp        time.Time `json:"timestamp"`
}

// notifyForeignMentionToPeer tells mentionedUserID's home server that
// mentioningReedID (authored here) mentions them.
func (h *Handlers) notifyForeignMentionToPeer(ctx context.Context, mentioningReedID, mentionedUserID string, ts time.Time) error {
	_, homeServerID, ok := identity.ParseIdentityID(identity.IdentityID(mentionedUserID))
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
	payload := relayMentionNotifyPayload{
		MentioningReedID: mentioningReedID,
		MentionedUserID:  mentionedUserID,
		Timestamp:        ts,
	}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/mention-notify", payload, nil)
	return err
}

// MentionNotifyFromPeer is leg 18's home-server handler: an established
// peer is telling us one of its reeds mentions one of our local users.
func (h *Handlers) MentionNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayMentionNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.MentioningReedID = strings.TrimSpace(req.MentioningReedID)
	req.MentionedUserID = strings.TrimSpace(req.MentionedUserID)
	if req.MentioningReedID == "" || req.MentionedUserID == "" {
		writeResponse(w, http.StatusBadRequest, "mentioning_reed_id and mentioned_user_id are required")
		return
	}

	// Loop-prevention: a peer may only notify us of mentions in reeds IT
	// actually authors — never claim a mention on behalf of a third
	// server. This server can only be "home" for users it hosts locally.
	_, mentioningServerID, _, mentioningOK := identity.ParseKeyFingerprint(identity.IdentityID(req.MentioningReedID))
	if !mentioningOK || mentioningServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "mentioning_reed_id does not belong to the calling peer")
		return
	}
	mentionedBareUserID, mentionedServerID, mentionedOK := identity.ParseIdentityID(identity.IdentityID(req.MentionedUserID))
	if !mentionedOK || mentionedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "mentioned_user_id is not local to this server")
		return
	}

	// Defense-in-depth: same "is this a real, live local user" gate
	// SignReed applies to a local mention target — self-mention filtering
	// already happened on O's side (ExtractMentions). This server's own
	// row in `servers` (seeded at boot, self=TRUE) is what makes
	// MentionTargetValid's servers-EXISTS check pass here.
	valid, err := h.services.db.MentionTargetValid(r.Context(), mentionedBareUserID, mentionedServerID)
	if err != nil {
		log.Error().Err(err).Str("mentionedUserID", req.MentionedUserID).Msg("Error validating foreign mention target")
		internalServerError(w)
		return
	}
	if !valid {
		writeResponse(w, http.StatusBadRequest, "Mentioned user not found")
		return
	}

	// Foreign mentioning reed needs a reed_identities row before the FK'd
	// insert below can succeed — same low "legitimate reference" bar
	// UpsertReedIdentity already applies for foreign echoes/relay requests.
	if err := h.services.db.UpsertReedIdentity(r.Context(), req.MentioningReedID); err != nil {
		log.Error().Err(err).Str("mentioningReedID", req.MentioningReedID).Msg("Failed to upsert mentioning reed identity")
		internalServerError(w)
		return
	}
	if err := h.services.db.InsertMentionRow(r.Context(), req.MentioningReedID, req.MentionedUserID); err != nil {
		log.Error().Err(err).Str("mentioningReedID", req.MentioningReedID).Str("mentionedUserID", req.MentionedUserID).Str("peerServerID", peerServerID).Msg("Failed to insert foreign mention")
		internalServerError(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// //////////////////////////////////////// //
//   Leg 13: reply-removal-notify (O -> H)  //
// //////////////////////////////////////// //
//
// Removal-side counterpart of leg 11: a reply to a foreign reed removed
// on O leaves a stale reference on H forever without this — H's
// reed_replies row lingers, so its reply-count and thread listing both
// keep counting/showing content that no longer exists.

type relayReplyRemovalNotifyPayload struct {
	ParentReedID string `json:"parent_reed_id"`
	ReplyReedID  string `json:"reply_reed_id"`
}

// notifyForeignReplyRemovalToPeer tells parentReedID's home server that
// replyReedID (removed here) no longer replies to it.
func (h *Handlers) notifyForeignReplyRemovalToPeer(ctx context.Context, parentReedID, replyReedID string) error {
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(parentReedID))
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
	payload := relayReplyRemovalNotifyPayload{ParentReedID: parentReedID, ReplyReedID: replyReedID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/reply-removal-notify", payload, nil)
	return err
}

// ReplyRemovalNotifyFromPeer is leg 13's home-server handler: an
// established peer is telling us a reply of theirs to one of our reeds
// was removed.
func (h *Handlers) ReplyRemovalNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayReplyRemovalNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ParentReedID = strings.TrimSpace(req.ParentReedID)
	req.ReplyReedID = strings.TrimSpace(req.ReplyReedID)
	if req.ParentReedID == "" || req.ReplyReedID == "" {
		writeResponse(w, http.StatusBadRequest, "parent_reed_id and reply_reed_id are required")
		return
	}

	// Loop-prevention: this server can only be "home" for reeds it hosts
	// locally, and a peer may only notify us about removal of a reply IT
	// actually authors — never claim removal on behalf of a third server.
	_, parentServerID, _, parentOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ParentReedID))
	if !parentOK || parentServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "parent_reed_id is not local to this server")
		return
	}
	_, replyServerID, _, replyOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ReplyReedID))
	if !replyOK || replyServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "reply_reed_id does not belong to the calling peer")
		return
	}

	deleted, err := h.services.db.DeleteForeignReplyReference(r.Context(), req.ReplyReedID)
	if err != nil {
		log.Error().Err(err).Str("parentReedID", req.ParentReedID).Str("replyReedID", req.ReplyReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign reply removal notify")
		internalServerError(w)
		return
	}

	if deleted {
		replyTargets, err := h.services.db.ReplyCountNotifyTargets(r.Context(), req.ParentReedID)
		if err != nil {
			log.Error().Err(err).Str("parentReedID", req.ParentReedID).Msg("Failed to resolve reply count targets after foreign reply removal")
		} else {
			for _, t := range replyTargets {
				h.broadcastChan <- realtime.BroadcastMessage{
					Type:   realtime.ReplyCountChanged,
					UserID: t.CanonicalAuthorID(),
					ReedID: t.ReedID,
				}
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// ///////////////////////////////////////// //
//   Leg 14: echo-removal-notify (O -> H)    //
// ///////////////////////////////////////// //
//
// Removal-side counterpart of leg 12, same reasoning as leg 13 for
// replies: without this, H's reed_echoes row for a removed foreign echo
// lingers forever, so its echo count and chorus both keep counting/
// showing content the echoing server has already removed.

type relayEchoRemovalNotifyPayload struct {
	EchoedReedID  string `json:"echoed_reed_id"`
	EchoingReedID string `json:"echoing_reed_id"`
}

// notifyForeignEchoRemovalToPeer tells echoedReedID's home server that
// echoingReedID (removed here) no longer echoes it.
func (h *Handlers) notifyForeignEchoRemovalToPeer(ctx context.Context, echoedReedID, echoingReedID string) error {
	_, homeServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(echoedReedID))
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
	payload := relayEchoRemovalNotifyPayload{EchoedReedID: echoedReedID, EchoingReedID: echoingReedID}
	_, err = h.callPeerRelayEndpoint(ctx, homeServerID, peer.BaseURL, "/api/federation/relay/echo-removal-notify", payload, nil)
	return err
}

// EchoRemovalNotifyFromPeer is leg 14's home-server handler: an
// established peer is telling us an echo of theirs, of one of our reeds,
// was removed.
func (h *Handlers) EchoRemovalNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayEchoRemovalNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.EchoedReedID = strings.TrimSpace(req.EchoedReedID)
	req.EchoingReedID = strings.TrimSpace(req.EchoingReedID)
	if req.EchoedReedID == "" || req.EchoingReedID == "" {
		writeResponse(w, http.StatusBadRequest, "echoed_reed_id and echoing_reed_id are required")
		return
	}

	// Loop-prevention: this server can only be "home" for reeds it hosts
	// locally, and a peer may only notify us about removal of an echo IT
	// actually authors — never claim removal on behalf of a third server.
	_, echoedServerID, _, echoedOK := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoedReedID))
	if !echoedOK || echoedServerID != h.services.db.GetServerID() {
		writeResponse(w, http.StatusBadRequest, "echoed_reed_id is not local to this server")
		return
	}
	_, echoingServerID, _, echoingOK := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoingReedID))
	if !echoingOK || echoingServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "echoing_reed_id does not belong to the calling peer")
		return
	}

	deleted, err := h.services.db.DeleteForeignEchoReference(r.Context(), req.EchoingReedID)
	if err != nil {
		log.Error().Err(err).Str("echoedReedID", req.EchoedReedID).Str("echoingReedID", req.EchoingReedID).Str("peerServerID", peerServerID).Msg("Failed to handle foreign echo removal notify")
		internalServerError(w)
		return
	}

	if deleted {
		echoedAuthorBareID, _, bareEchoedReedID, _ := identity.ParseKeyFingerprint(identity.IdentityID(req.EchoedReedID))
		h.broadcastChan <- realtime.BroadcastMessage{
			Type:   realtime.EchoCountChanged,
			UserID: string(identity.CanonicalID(echoedServerID, echoedAuthorBareID)),
			ReedID: bareEchoedReedID,
		}
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	if req.ReedID == "" {
		writeResponse(w, http.StatusBadRequest, "reed_id is required")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	if err := h.realtimeRelay.HandleHolderNotify(r.Context(), req.ReedID, peerServerID); err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle holder notify")
		internalServerError(w)
		return
	}
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
func (h *Handlers) relayFallbackRequestToPeer(ctx context.Context, peerServerID, reedID, requesterUserID, localRequestID string) (realtime.ForeignRequestResult, string, error) {
	peer, err := h.services.db.GetServerByID(ctx, peerServerID)
	if err != nil {
		return realtime.ForeignRequestReedNotFound, "", err
	}
	if peer == nil {
		return realtime.ForeignRequestReedNotFound, "", nil
	}

	payload := relayFallbackRequestPayload{
		ReedID:          reedID,
		RequesterUserID: requesterUserID,
		PeerRequestID:   localRequestID,
	}
	var respBody relayFallbackRequestResponse
	status, err := h.callPeerRelayEndpoint(ctx, peerServerID, peer.BaseURL, "/api/federation/relay/fallback-request", payload, &respBody)
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
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	if req.ReedID == "" || req.RequesterUserID == "" {
		writeResponse(w, http.StatusBadRequest, "reed_id and requester_user_id are required")
		return
	}

	// Inverse of leg 1's loop-prevention: the CALLER must own reedID —
	// this stops any peer from asking us to hand back content on behalf
	// of a reed it doesn't actually author.
	_, embeddedServerID, _, parseOK := identity.ParseKeyFingerprint(identity.IdentityID(req.ReedID))
	if !parseOK || embeddedServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "reed_id is not owned by the calling peer")
		return
	}
	if !peerRequestIDMatchesPeer(req.PeerRequestID, peerServerID) {
		writeResponse(w, http.StatusBadRequest, "peer_request_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	result, peerEventID, err := h.realtimeRelay.HandleForeignFallbackRequest(r.Context(), req.ReedID, peerServerID, req.RequesterUserID, req.PeerRequestID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle fallback request")
		internalServerError(w)
		return
	}
	switch result {
	case realtime.ForeignRequestReedNotFound:
		writeResponse(w, http.StatusNotFound, "Reed not found")
	case realtime.ForeignRequestReedNotHeld:
		writeResponse(w, http.StatusConflict, "Reed is not currently held")
	default:
		writeResponse(w, http.StatusOK, relayFallbackRequestResponse{PeerEventID: peerEventID, Status: "ack"})
	}
}

// ///////////////////////////////////////// //
//   Leg 17: new-reed-notify (H -> O)        //
// ///////////////////////////////////////// //
//
// H (authorID's home server) just fanned out a newly-published reed to a
// foreign profile subscriber and, alongside recording foreign_relay_requests
// as usual, pushes this single (reed_id, peer_event_id) pair to O — the
// viewer's own server — so O can register it against the durable profile
// subscription it already holds for that viewer (created back when
// SUBSCRIBE_PROFILE first ran leg 1b's backfill). Without this push, O only
// ever learns about reeds that existed at that original subscribe time;
// anything published afterward has no foreign_pending_events row on O, so
// leg 2's eventual deliver call has nothing to resolve peer_event_id
// against and 404s. Fire-and-forget, same as leg 15: a lost notification
// just means this one viewer misses the live push and picks the reed up on
// their next resubscribe or reload, same as if this leg didn't exist.

type relayNewReedNotifyPayload struct {
	AuthorID        string `json:"author_id"`
	RequesterUserID string `json:"requester_user_id"`
	ReedID          string `json:"reed_id"`
	PeerEventID     string `json:"peer_event_id"`
}

// notifyNewReedToPeer is the ForeignNewReedNotifyHook implementation (leg
// 17, H's side): pushes one newly-published reed's event to
// requestingServerID for one of its own users, requesterUserID, who holds a
// durable profile subscription to authorID (local to this server).
func (h *Handlers) notifyNewReedToPeer(ctx context.Context, requestingServerID, authorID, requesterUserID, reedID, peerEventID string) error {
	peer, err := h.services.db.GetServerByID(ctx, requestingServerID)
	if err != nil {
		return err
	}
	if peer == nil {
		return nil
	}
	payload := relayNewReedNotifyPayload{
		AuthorID:        authorID,
		RequesterUserID: requesterUserID,
		ReedID:          reedID,
		PeerEventID:     peerEventID,
	}
	_, err = h.callPeerRelayEndpoint(ctx, requestingServerID, peer.BaseURL, "/api/federation/relay/new-reed-notify", payload, nil)
	return err
}

// RelayNewReedNotifyFromPeer is leg 17's viewer-server handler: an
// established peer — one of our own local users' followed/viewed author's
// home server — is pushing a single new reed's event for us to register.
// No ownership check on authorID beyond "caller is an authenticated peer"
// is needed (mirrors leg 15's HolderNotifyFromPeer): recording this mapping
// doesn't require authorID to belong to the caller in any stronger sense
// than "the caller is the one home server able to answer the reed's live
// fanout for it", which HandleForeignNewReedNotify itself further narrows
// by only registering against a subscription requesterUserID (a real local
// user) already holds for authorID.
func (h *Handlers) RelayNewReedNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req relayNewReedNotifyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.AuthorID = strings.TrimSpace(req.AuthorID)
	req.RequesterUserID = strings.TrimSpace(req.RequesterUserID)
	req.ReedID = strings.TrimSpace(req.ReedID)
	req.PeerEventID = strings.TrimSpace(req.PeerEventID)
	if req.AuthorID == "" || req.RequesterUserID == "" || req.ReedID == "" || req.PeerEventID == "" {
		writeResponse(w, http.StatusBadRequest, "author_id, requester_user_id, reed_id, and peer_event_id are required")
		return
	}

	// requester_user_id must belong to the calling peer — same guard as
	// leg 1b/8's requester checks — so a peer can only ever register
	// events on behalf of its own local users, never someone else's.
	_, requesterServerID, requesterOK := identity.ParseIdentityID(identity.IdentityID(req.RequesterUserID))
	if !requesterOK || requesterServerID != peerServerID {
		writeResponse(w, http.StatusBadRequest, "requester_user_id does not belong to the calling peer")
		return
	}

	if h.realtimeRelay == nil {
		internalServerError(w)
		return
	}
	found, err := h.realtimeRelay.HandleForeignNewReedNotify(r.Context(), req.AuthorID, peerServerID, req.RequesterUserID, req.ReedID, req.PeerEventID)
	if err != nil {
		log.Error().Err(err).Str("reedID", req.ReedID).Str("peerServerID", peerServerID).Msg("Failed to handle new reed notify")
		internalServerError(w)
		return
	}
	if !found {
		writeResponse(w, http.StatusNotFound, "No active subscription for this author/requester pair")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
