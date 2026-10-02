//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"syrinx/observability/metrics"
)

// A vouch about a user of another server is only stored once that server
// has accepted a reference to it, so it can be found from the subject's
// side. Withdrawals go the same way. Both calls are synchronous.

// vouchDeliveryTimeout bounds one call to the subject's server; the user
// is waiting on the other end.
const vouchDeliveryTimeout = 10 * time.Second

// errVouchSubjectUnreachable means the subject's server could not be asked
// (unknown, unreachable, or failing); trying again later may work.
var errVouchSubjectUnreachable = errors.New("subject's server could not be reached")

// errVouchSubjectRefused means the subject's server checked the vouch and
// will not accept it; sending it again will not help.
var errVouchSubjectRefused = errors.New("subject's server refused the vouch")

type relayVouchReferencePayload struct {
	Cert VouchCert `json:"cert"`
}

type relayVouchWithdrawalPayload struct {
	VouchID       string          `json:"vouch_id"`
	VoucherUserID string          `json:"voucher_user_id"`
	Withdrawal    VouchWithdrawal `json:"withdrawal"`
}

// deliverVouchToSubject asks the subject's server to accept a reference to
// cert and waits for its answer.
func (h *Handlers) deliverVouchToSubject(ctx context.Context, cert VouchCert) error {
	return h.callSubjectServer(ctx, cert.SubjectUserID, "/api/federation/relay/vouch-reference",
		relayVouchReferencePayload{Cert: cert})
}

// deliverVouchWithdrawalToSubject asks the subject's server to drop its
// reference to a withdrawn vouch and waits for its answer.
func (h *Handlers) deliverVouchWithdrawalToSubject(ctx context.Context, subjectUserID string, payload relayVouchWithdrawalPayload) error {
	return h.callSubjectServer(ctx, subjectUserID, "/api/federation/relay/vouch-withdrawal", payload)
}

func (h *Handlers) callSubjectServer(ctx context.Context, subjectUserID, path string, payload any) error {
	_, serverID, ok := parseIdentityID(identityID(subjectUserID))
	if !ok {
		return fmt.Errorf("%w: malformed subject id", errVouchSubjectRefused)
	}
	peer, err := h.services.db.GetServerByID(ctx, serverID)
	if err != nil || peer == nil {
		return fmt.Errorf("%w: %s is not a connected peer", errVouchSubjectUnreachable, serverID)
	}
	ctx, cancel := context.WithTimeout(ctx, vouchDeliveryTimeout)
	defer cancel()
	status, err := h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, path, payload, nil)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %v", errVouchSubjectUnreachable, err)
	case status >= 200 && status < 300:
		return nil
	case status >= 400 && status < 500:
		return fmt.Errorf("%w: %s answered %d", errVouchSubjectRefused, serverID, status)
	default:
		return fmt.Errorf("%w: %s answered %d", errVouchSubjectUnreachable, serverID, status)
	}
}

// writeVouchDeliveryError answers the voucher's client when the subject's
// server did not accept, naming which kind of failure it was.
func writeVouchDeliveryError(w http.ResponseWriter, err error) {
	if errors.Is(err, errVouchSubjectRefused) {
		writeResponse(w, http.StatusUnprocessableEntity, "The subject's server refused this verification")
		return
	}
	writeResponse(w, http.StatusBadGateway, "Could not reach the subject's server")
}

// VouchReferenceFromPeer handles a peer telling this server one of its
// users vouched for one of ours. The cert is verified in full and then
// discarded: only the reference is kept, and readers fetch the cert from
// the voucher's server.
func (h *Handlers) VouchReferenceFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fail := func(status int, msg string) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "vouch-reference", false)
		writeResponse(w, status, msg)
	}

	var req relayVouchReferencePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(http.StatusBadRequest, "Invalid request body")
		return
	}
	cert := req.Cert

	status, msg := h.checkForeignVouch(r.Context(), peerServerID, cert)
	if status != 0 {
		log.Info().Str("vouchID", cert.ID).Str("peerServerID", peerServerID).Str("reason", msg).Msg("Refused vouch reference from peer")
		fail(status, msg)
		return
	}
	if err := h.services.db.InsertVouchReference(r.Context(), cert.ID, cert.SubjectUserID, cert.SubjectKeyID, peerServerID); err != nil {
		log.Error().Err(err).Str("vouchID", cert.ID).Msg("Failed to store vouch reference")
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	h.broadcastChan <- realtimeBroadcastMessage{
		Type:    realtimeVouchCreated,
		UserID:  cert.SubjectUserID,
		VouchID: cert.ID,
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "vouch-reference", true)
	w.WriteHeader(http.StatusNoContent)
}

// checkForeignVouch runs every check a reference has to pass. A non-zero
// status is the 4xx answer for a vouch that will never be accepted, or a
// 5xx when this server could not finish checking.
func (h *Handlers) checkForeignVouch(ctx context.Context, peerServerID string, cert VouchCert) (int, string) {
	db := h.services.db

	subjectBareID, subjectServerID, ok := parseIdentityID(identityID(cert.SubjectUserID))
	if !ok || subjectServerID != db.GetServerID() {
		return http.StatusBadRequest, "subject is not a user of this server"
	}
	valid, err := db.MentionTargetValid(ctx, subjectBareID, subjectServerID)
	if err != nil {
		return http.StatusInternalServerError, "could not look up the subject"
	}
	if !valid {
		return http.StatusNotFound, "subject not found"
	}
	if !keyBelongsTo(cert.SubjectKeyID, cert.SubjectUserID) {
		return http.StatusBadRequest, "subject key is not the subject's"
	}
	subjectKey, err := db.GetPublicKey(ctx, cert.SubjectKeyID)
	if err != nil {
		return http.StatusInternalServerError, "could not load the subject key"
	}
	if subjectKey == nil {
		return http.StatusNotFound, "subject key not found"
	}
	if subjectKey.Revoked {
		return http.StatusConflict, "subject key is revoked"
	}

	// A peer only announces vouches its own users made.
	_, voucherServerID, ok := parseIdentityID(identityID(cert.VoucherUserID))
	if !ok || voucherServerID != peerServerID {
		return http.StatusBadRequest, "voucher is not a user of the calling server"
	}
	if msg := validateVouchID(cert.ID, cert.VoucherUserID, peerServerID); msg != "" {
		return http.StatusBadRequest, msg
	}
	if !keyBelongsTo(cert.VoucherKeyID, cert.VoucherUserID) {
		return http.StatusBadRequest, "voucher key is not the voucher's"
	}

	voucherKey, err := h.resolvePublicKey(ctx, cert.VoucherKeyID)
	if err != nil {
		return http.StatusInternalServerError, "could not fetch the voucher key"
	}
	if voucherKey == nil {
		return http.StatusBadRequest, "voucher key could not be resolved"
	}
	userSig, err := base64Decode(cert.UserSignature.Armor)
	if err != nil {
		return http.StatusBadRequest, "invalid signature encoding"
	}
	userPayload := buildVouchUserPayload(cert.VoucherKeyID, cert.SubjectKeyID, cert.Note)
	if err := h.services.crypto.verifySignature(string(userPayload), userSig, voucherKey.Armor); err != nil {
		return http.StatusBadRequest, "voucher signature does not verify"
	}

	serverPayload := func(fingerprint string) []byte {
		return buildVouchServerPayload(cert.SubjectKeyID, fingerprint, cert.UserSignature.Armor, cert.ServerSignature.SignedAt)
	}
	return h.checkPeerCountersignature(ctx, peerServerID, cert.ServerSignature, serverPayload)
}

// checkPeerCountersignature verifies a countersignature made by the
// calling peer against the key pinned when it was approved.
func (h *Handlers) checkPeerCountersignature(ctx context.Context, peerServerID string, sig ServerSignature, payload func(fingerprint string) []byte) (int, string) {
	fingerprint, signerServerID, ok := parseIdentityID(identityID(sig.ID))
	if !ok || signerServerID != peerServerID {
		return http.StatusBadRequest, "countersignature is not the calling server's"
	}
	pinned, armor, err := h.services.db.VerifyFederationPeer(ctx, peerServerID, fingerprint)
	if err != nil {
		return http.StatusInternalServerError, "could not load the calling server's key"
	}
	if !pinned {
		return http.StatusBadRequest, "countersignature key is not the one pinned for the calling server"
	}
	sigArmor, err := base64Decode(sig.Armor)
	if err != nil {
		return http.StatusBadRequest, "invalid countersignature encoding"
	}
	if err := h.services.crypto.verifySignature(string(payload(fingerprint)), sigArmor, armor); err != nil {
		return http.StatusBadRequest, "countersignature does not verify"
	}
	return 0, ""
}

// VouchWithdrawalFromPeer handles a peer telling this server a vouch about
// one of our users was withdrawn. The signed withdrawal is checked before
// the reference is dropped; an unknown reference is already gone.
func (h *Handlers) VouchWithdrawalFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fail := func(status int, msg string) {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "vouch-withdrawal", false)
		writeResponse(w, status, msg)
	}

	var req relayVouchWithdrawalPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(http.StatusBadRequest, "Invalid request body")
		return
	}
	req.VouchID = strings.TrimSpace(req.VouchID)
	if msg := validateVouchID(req.VouchID, req.VoucherUserID, peerServerID); msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	voucherKeyID := req.Withdrawal.UserSignature.ID
	if !keyBelongsTo(voucherKeyID, req.VoucherUserID) {
		fail(http.StatusBadRequest, "withdrawal key is not the voucher's")
		return
	}
	voucherKey, err := h.resolvePublicKey(r.Context(), voucherKeyID)
	if err != nil {
		fail(http.StatusInternalServerError, "could not fetch the voucher key")
		return
	}
	if voucherKey == nil {
		fail(http.StatusBadRequest, "voucher key could not be resolved")
		return
	}
	userSig, err := base64Decode(req.Withdrawal.UserSignature.Armor)
	if err != nil {
		fail(http.StatusBadRequest, "invalid signature encoding")
		return
	}
	if err := h.services.crypto.verifySignature(string(buildVouchWithdrawalUserPayload(req.VouchID)), userSig, voucherKey.Armor); err != nil {
		fail(http.StatusBadRequest, "withdrawal signature does not verify")
		return
	}
	serverPayload := func(fingerprint string) []byte {
		return buildVouchWithdrawalServerPayload(req.VouchID, fingerprint, req.Withdrawal.UserSignature.Armor, req.Withdrawal.ServerSignature.SignedAt)
	}
	if status, msg := h.checkPeerCountersignature(r.Context(), peerServerID, req.Withdrawal.ServerSignature, serverPayload); status != 0 {
		fail(status, msg)
		return
	}

	if err := h.services.db.DeleteVouchReference(r.Context(), req.VouchID, peerServerID); err != nil {
		log.Error().Err(err).Str("vouchID", req.VouchID).Msg("Failed to drop vouch reference")
		fail(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "vouch-withdrawal", true)
	w.WriteHeader(http.StatusNoContent)
}
