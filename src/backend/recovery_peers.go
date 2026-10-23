//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "syrinx/proto"
)

// Peers, and what devices hold of their users and reeds, come back from
// devices too: first the peer records this server countersigned, then the
// peers' users and reeds, checked against the keys those records name.

// ReportServers handles POST /api/recovery/servers.
func (h *Handlers) ReportServers(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var msg pb.RecoveryServersRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(msg.GetServers()) > maxRecoveryRecordsBatch {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d servers per request", maxRecoveryRecordsBatch))
		return
	}
	out := &pb.RecoveryRecordsResponse{}
	for _, rec := range msg.GetServers() {
		refusal, err := h.restorePeerRecord(r.Context(), rec)
		if err != nil {
			h.services.log.GetLogger(r.Context()).Error().Err(err).Str("peerServerID", rec.GetId()).Msg("Recovery peer record failed")
			h.metrics.ServerRecovery(r.Context(), "servers", false)
			internalServerError(w)
			return
		}
		if refusal != "" {
			out.Results = append(out.Results, &pb.RecoveryRecordResult{Status: recoveryRecordRefused, Reason: refusal})
			continue
		}
		out.Results = append(out.Results, &pb.RecoveryRecordResult{Status: recoveryRecordStored})
	}
	h.metrics.ServerRecovery(r.Context(), "servers", true)
	writeResponse(w, http.StatusOK, out)
}

// restorePeerRecord checks rec is a record this server countersigned, and
// that the peer key it comes with is the one it names, then restores the
// peer unless a newer record of it is already held. A non-empty refusal
// says why rec was refused.
func (h *Handlers) restorePeerRecord(ctx context.Context, rec *pb.FederatedServerInfo) (refusal string, err error) {
	self := h.services.db.GetServerID()
	sig := rec.GetServerSignature()
	fp, sigServerID, ok := parseIdentityID(identityID(sig.GetId()))
	if !ok || sigServerID != self || sig.GetArmor() == "" || sig.GetSignedAt() == 0 {
		return "not countersigned by this server", nil
	}
	status := rec.GetStatus()
	if rec.GetId() == "" || rec.GetId() == self || (status != peerRecordConnected && status != peerRecordRevoked && status != peerRecordPurged) {
		return "not a peer record", nil
	}
	signedAt := timeFromUnix(sig.GetSignedAt())
	armor, err := h.services.db.GetServerKeyArmorAt(ctx, fp, signedAt)
	if err != nil {
		return "", err
	}
	if armor == "" {
		return "unknown server key " + fp, nil
	}
	payload := buildPeerRecordPayload(self, rec.GetId(), rec.GetName(), rec.GetKeyId(), rec.GetBaseUrl(), rec.GetFrontendUrl(), status, signedAt)
	if err := h.services.crypto.verifySignature(ctx, string(payload), sig.GetArmor(), armor); err != nil {
		return "bad countersignature", nil
	}

	peerFP, keyServerID, ok := parseIdentityID(identityID(rec.GetKeyId()))
	if !ok || keyServerID != rec.GetId() {
		return "the record's key is not the peer's", nil
	}
	keyArmor := rec.GetKeyArmor()
	if !h.armorIsKey(keyArmor, peerFP) {
		keyArmor = ""
		if rec.GetBaseUrl() != "" {
			fetched, err := h.fetchPeerServerKeyArmor(ctx, rec.GetBaseUrl(), rec.GetId(), peerFP)
			if err == nil && h.armorIsKey(fetched, peerFP) {
				keyArmor = fetched
			}
		}
		if keyArmor == "" {
			return "the peer's server key is missing", nil
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	keySig, err := h.countersign(ctx, buildPublicKeyPayload(self, rec.GetKeyId(), rec.GetKeyId(), peerFP, keyArmor, now), now)
	if err != nil {
		return "", err
	}
	srv := FederatedServerInfo{
		ID: rec.GetId(), Name: rec.GetName(), KeyID: rec.GetKeyId(),
		BaseURL: rec.GetBaseUrl(), FrontendURL: rec.GetFrontendUrl(), Revoked: status != peerRecordConnected, Purged: status == peerRecordPurged,
	}
	return "", h.services.db.RestorePeer(ctx, restoredPeer{
		FederatedServerInfo: srv,
		KeyArmor:            keyArmor,
		KeySignature:        keySig,
		Record:              ServerSignature{ID: sig.GetId(), Armor: sig.GetArmor(), SignedAt: signedAt},
		RecordFields:        srv.recordFields(),
	})
}

// armorIsKey reports whether armor is the key with fingerprint fp.
func (h *Handlers) armorIsKey(armor, fp string) bool {
	if armor == "" {
		return false
	}
	got, err := h.services.crypto.extractFingerprintFromArmor(armor)
	return err == nil && strings.EqualFold(got, fp)
}

// peerKeyLookup finds peerID's server key by fingerprint: the one restored
// with its record, or, for one it has since rotated away, from the peer.
func (h *Handlers) peerKeyLookup(peerID string) recoveryServerKeyLookup {
	return func(ctx context.Context, fp string, _ time.Time) (string, error) {
		key, err := h.services.db.GetPublicKey(ctx, string(canonicalID(peerID, fp)))
		if err != nil {
			return "", err
		}
		if key != nil {
			return key.Armor, nil
		}
		baseURL, err := h.services.db.GetServerBaseURLAnyState(ctx, peerID)
		if err != nil || baseURL == "" {
			return "", err
		}
		armor, err := h.fetchPeerServerKeyArmor(ctx, baseURL, peerID, fp)
		if err != nil {
			return "", fmt.Errorf("fetch %s's key %s: %w", peerID, fp, err)
		}
		if !h.armorIsKey(armor, fp) {
			return "", nil
		}
		return armor, nil
	}
}

// refuseUnknownPeer answers 422, which the client skips, unless serverID is
// a peer this server holds and hasn't revoked.
func (h *Handlers) refuseUnknownPeer(w http.ResponseWriter, r *http.Request, serverID string) bool {
	active, err := h.services.db.IsActivePeer(r.Context(), serverID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if !active {
		writeError(w, http.StatusUnprocessableEntity, serverID+" isn't a peer of this server")
		return true
	}
	return false
}

// reportPeerUser restores what a device holds of a peer's user: their
// identity and keys, checked against the peer's server keys. A peer's users
// are never accounts here, so no profile or username is stored.
func (h *Handlers) reportPeerUser(w http.ResponseWriter, r *http.Request, peerID string, req recoveryPeerIdentityRequest) bool {
	if h.refuseUnknownPeer(w, r, peerID) {
		return false
	}
	active, keys, err := flattenKeysNest(r.Context(), req.Profile, req.Key, peerID, h.peerKeyLookup(peerID), h.services.crypto)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	for _, k := range keys {
		sig := ServerSignature{
			ID:       string(canonicalID(peerID, k.Key.ServerSignature.Fingerprint)),
			Armor:    k.Key.ServerSignature.Armor,
			SignedAt: k.Key.ServerSignature.Timestamp.UTC().Truncate(time.Second),
		}
		if err := h.services.db.CachePeerUserKey(r.Context(), k.Key.Fingerprint, req.Profile.ID, peerID, k.Key.Armor, sig); err != nil {
			h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", req.Profile.ID).Msg("Recovery peer user key failed")
			internalServerError(w)
			return false
		}
	}
	h.wakeRecoveryRecords(r, req.Profile.ID)
	req.Profile.ActiveKeyFingerprint = active.Key.Fingerprint
	writeResponse(w, http.StatusOK, pbRecoveryProfile(req.Profile))
	return true
}

// reportPeerReed restores what a device holds of a peer's reed: that it
// exists, that the reporter holds it, and what of ours it replies to,
// echoes or mentions. Its content never lives here.
func (h *Handlers) reportPeerReed(w http.ResponseWriter, r *http.Request, peerID, caller string, req recoveryReedRequest) bool {
	if h.refuseUnknownPeer(w, r, peerID) {
		return false
	}
	if err := verifyRecoveryReedCountersig(r.Context(), req, peerID, h.peerKeyLookup(peerID), h.services.crypto); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if err := savePeerRecoveryReed(r.Context(), h.services.db.db, h.services.db.GetServerID(), peerID, req, caller); err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("reedID", req.ReedID).Msg("Recovery peer reed failed")
		internalServerError(w)
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// savePeerRecoveryReed stores a peer's reed as live federation does: its
// identity, the reporter's holding, and the links that stay on this server.
func savePeerRecoveryReed(ctx context.Context, db *sql.DB, selfID, peerID string, req recoveryReedRequest, reporter string) error {
	author, ok := authorOf(identityID(req.ReedID))
	if !ok {
		return fmt.Errorf("malformed reed id: %s", req.ReedID)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertReedIdentityOf(ctx, tx, req.ReedID, peerID, string(author)); err != nil {
		return fmt.Errorf("insert reed identity: %w", err)
	}
	signedAt := req.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	if err := restoreReedLinksTx(ctx, tx, selfID, req.ReedID, author, signedAt, req.Links, false); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		SELECT $1, $2::VARCHAR
		WHERE NOT EXISTS (SELECT 1 FROM user_blocks WHERE user_id = $3 AND blocked_user_id = $2::VARCHAR)
		ON CONFLICT DO NOTHING
	`, req.ReedID, reporter, author); err != nil {
		return fmt.Errorf("insert reed allocation: %w", err)
	}
	return tx.Commit()
}

// restoredPeer is a verified peer record, with the peer's key countersigned
// here as approval does.
type restoredPeer struct {
	FederatedServerInfo
	KeyArmor     string
	KeySignature ServerSignature
	Record       ServerSignature
	RecordFields string
}

// RestorePeer stores p unless a record of the peer signed at the same time
// or later is already held. A restored peer's delivery starts now: nothing
// from before the wipe is sent again.
func (s *DataService) RestorePeer(ctx context.Context, p restoredPeer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var heldSigID sql.NullInt64
	var heldAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT m.record_signature_id, ss.signed_at
		FROM server_mesh m
		LEFT JOIN server_signatures ss ON ss.id = m.record_signature_id
		WHERE m.server_id = $1
		FOR UPDATE OF m
	`, p.ID).Scan(&heldSigID, &heldAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if heldAt.Valid && !p.Record.SignedAt.After(heldAt.Time.UTC()) {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, base_url, frontend_url)
		VALUES ($1, $2, FALSE, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name,
			base_url = EXCLUDED.base_url, frontend_url = EXCLUDED.frontend_url
	`, p.ID, p.Name, p.BaseURL, p.FrontendURL); err != nil {
		return fmt.Errorf("restore peer server: %w", err)
	}
	var keyHeld bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public_keys WHERE id = $1)`, p.KeyID).Scan(&keyHeld); err != nil {
		return err
	}
	if !keyHeld {
		keySigID, err := insertServerSignature(ctx, tx, p.KeySignature.ID, p.KeySignature.Armor, p.KeySignature.SignedAt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO public_keys (id, owner, armor, created_at, server_signature_id)
			VALUES ($1, NULL, $2, $3, $4)
		`, p.KeyID, p.KeyArmor, p.KeySignature.SignedAt, keySigID); err != nil {
			return fmt.Errorf("restore peer key: %w", err)
		}
	}

	recordSigID, err := insertServerSignature(ctx, tx, p.Record.ID, p.Record.Armor, p.Record.SignedAt)
	if err != nil {
		return err
	}
	var revokedAt, purgedAt sql.NullTime
	if p.Revoked {
		revokedAt = sql.NullTime{Time: p.Record.SignedAt, Valid: true}
	}
	if p.Purged {
		purgedAt = revokedAt
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_mesh (server_id, key_id, approved_at, peer_approved_at, revoked_at, purged_at, record_signature_id, record_fields)
		VALUES ($1, $6, CURRENT_TIMESTAMP, CASE WHEN $2::timestamp IS NULL THEN CURRENT_TIMESTAMP END, $2, $5, $3, $4)
		ON CONFLICT (server_id) DO UPDATE SET connected = TRUE, key_id = EXCLUDED.key_id,
			revoked_at = EXCLUDED.revoked_at, revoked_by = NULL, revoked_reason = NULL, purged_at = EXCLUDED.purged_at,
			peer_approved_at = COALESCE(server_mesh.peer_approved_at, EXCLUDED.peer_approved_at),
			record_signature_id = EXCLUDED.record_signature_id, record_fields = EXCLUDED.record_fields
	`, p.ID, revokedAt, recordSigID, p.RecordFields, purgedAt, p.KeyID); err != nil {
		return fmt.Errorf("restore peering: %w", err)
	}
	if heldSigID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_signatures WHERE id = $1`, heldSigID.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsActivePeer reports whether serverID is a peer this server holds and
// hasn't revoked.
func (s *DataService) IsActivePeer(ctx context.Context, serverID string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM server_mesh WHERE server_id = $1 AND revoked_at IS NULL)
	`, serverID).Scan(&active)
	return active, err
}
