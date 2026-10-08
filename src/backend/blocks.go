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

	"github.com/gorilla/mux"
)

// ErrBlockConflict: a block already exists for the pair under different
// signatures.
var ErrBlockConflict = errors.New("block conflict")

const blockCertSelect = `
	SELECT b.user_id, b.blocked_user_id,
		us.public_key_id, us.signature,
		ss.private_key_id, ss.signature, ss.signed_at
	FROM user_blocks b
	JOIN user_signatures us ON us.id = b.user_signature_id
	JOIN server_signatures ss ON ss.id = b.server_signature_id`

type blockRowScanner interface {
	Scan(dest ...any) error
}

func scanBlockCert(row blockRowScanner) (*BlockCert, error) {
	cert := BlockCert{Type: identityTypeBlock}
	err := row.Scan(
		&cert.UserID, &cert.BlockedUserID,
		&cert.UserSignature.ID, &cert.UserSignature.Armor,
		&cert.ServerSignature.ID, &cert.ServerSignature.Armor, &cert.ServerSignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC()
	return &cert, nil
}

// GetBlock returns userID's block of blockedUserID, or nil.
func (s *DataService) GetBlock(ctx context.Context, userID, blockedUserID string) (*BlockCert, error) {
	cert, err := scanBlockCert(s.db.QueryRowContext(ctx,
		blockCertSelect+` WHERE b.user_id = $1 AND b.blocked_user_id = $2`,
		userID, blockedUserID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return cert, err
}

// ListBlocksByUser returns every block userID made, newest first.
func (s *DataService) ListBlocksByUser(ctx context.Context, userID string) ([]BlockCert, error) {
	rows, err := s.db.QueryContext(ctx,
		blockCertSelect+` WHERE b.user_id = $1 ORDER BY ss.signed_at DESC, b.blocked_user_id`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := []BlockCert{}
	for rows.Next() {
		cert, err := scanBlockCert(rows)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, *cert)
	}
	return blocks, rows.Err()
}

// InsertBlock stores a block once and, on first insert, removes the
// blocked user's follow of the blocking user and their allocations of the
// blocking user's reeds in the same transaction. dropped lists those reeds.
func (s *DataService) InsertBlock(ctx context.Context, cert BlockCert) (created bool, dropped []string, err error) {
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()

	existing, err := scanBlockCert(tx.QueryRowContext(ctx,
		blockCertSelect+` WHERE b.user_id = $1 AND b.blocked_user_id = $2 FOR UPDATE OF b`,
		cert.UserID, cert.BlockedUserID))
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return false, nil, err
	default:
		if existing.UserSignature != cert.UserSignature ||
			existing.ServerSignature.ID != cert.ServerSignature.ID ||
			existing.ServerSignature.Armor != cert.ServerSignature.Armor ||
			!existing.ServerSignature.SignedAt.Equal(cert.ServerSignature.SignedAt) {
			return false, nil, ErrBlockConflict
		}
		return false, nil, nil
	}

	userSigID, err := insertUserSignature(ctx, tx, cert.UserSignature.ID, cert.UserSignature.Armor)
	if err != nil {
		return false, nil, err
	}
	serverSigID, err := insertServerSignature(ctx, tx, cert.ServerSignature.ID, cert.ServerSignature.Armor, cert.ServerSignature.SignedAt)
	if err != nil {
		return false, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_blocks (
			user_id, blocked_user_id, public_key_id,
			user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5)
	`, cert.UserID, cert.BlockedUserID, cert.UserSignature.ID, userSigID, serverSigID); err != nil {
		return false, nil, fmt.Errorf("insert block: %w", err)
	}

	dropped, err = applyBlockEffectsTx(ctx, tx, cert.UserID, cert.BlockedUserID)
	if err != nil {
		return false, nil, err
	}
	return true, dropped, tx.Commit()
}

// applyBlockEffectsTx forces the blocked user's unfollow of the blocking user and
// drops their allocations of the blocking user's reeds. Only the rows this
// server keeps for each side exist, so it serves either server.
func applyBlockEffectsTx(ctx context.Context, tx *sql.Tx, userID, blockedUserID string) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2
	`, blockedUserID, userID); err != nil {
		return nil, fmt.Errorf("delete blocked user's following: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2
	`, userID, blockedUserID); err != nil {
		return nil, fmt.Errorf("delete blocked follower: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		DELETE FROM reed_allocations ra
		USING reed_identities ri
		WHERE ri.id = ra.reed_id AND ra.holder_user_id = $1 AND ri.author_id = $2
		RETURNING ra.reed_id
	`, blockedUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("drop blocked user's allocations: %w", err)
	}
	defer rows.Close()
	var dropped []string
	for rows.Next() {
		var reedID string
		if err := rows.Scan(&reedID); err != nil {
			return nil, err
		}
		dropped = append(dropped, reedID)
	}
	return dropped, rows.Err()
}

// DeleteBlock removes userID's block of blockedUserID. deleted is false
// when there was none.
func (s *DataService) DeleteBlock(ctx context.Context, userID, blockedUserID string) (deleted bool, err error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM user_blocks WHERE user_id = $1 AND blocked_user_id = $2
	`, userID, blockedUserID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// writeBlocked answers a request the block refuses: 403 with the cert.
func writeBlocked(w http.ResponseWriter, cert *BlockCert) {
	writeResponse(w, http.StatusForbidden, cert)
}

// BlockUser handles POST /users/{userID}/block: the caller signs a block of
// userID; this server countersigns it once and applies it.
func (h *Handlers) BlockUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	blockedUserID := strings.TrimSpace(mux.Vars(r)["userID"])
	if _, _, ok := parseIdentityID(identityID(blockedUserID)); !ok {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is invalid")
		return
	}
	if blockedUserID == userID {
		writeResponse(w, http.StatusBadRequest, "Cannot block yourself")
		return
	}
	if _, foreign := h.foreignServerOf(blockedUserID); foreign {
		writeResponse(w, http.StatusUnprocessableEntity, "Blocking users on other servers is not supported yet")
		return
	}

	values, err := parseFormData(r)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature := strings.TrimSpace(values.Get("signature"))
	bareFingerprint := strings.TrimSpace(values.Get("fingerprint"))
	if userSignature == "" || bareFingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `signature` and `fingerprint` are required")
		return
	}
	keyID := string(appendEntity(identityID(userID), bareFingerprint))

	removal, err := h.services.db.GetAccountRemoval(r.Context(), blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("blockedUserID", blockedUserID).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(removal))
		return
	}
	profile, err := h.services.db.GetUserProfile(r.Context(), blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("blockedUserID", blockedUserID).Msg("Error loading blocked user")
		internalServerError(w)
		return
	}
	if profile == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	existing, err := h.services.db.GetBlock(r.Context(), userID, blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Error loading block")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature.Armor != userSignature {
			writeResponse(w, http.StatusConflict, "Block already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, existing)
		return
	}

	pubKey, err := h.resolvePublicKey(r.Context(), keyID)
	if err != nil {
		log.Error().Err(err).Str("keyID", keyID).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(buildBlockUserPayload(userID, blockedUserID, keyID)), userSignature, pubKey.Armor); err != nil {
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverSignature, err := h.countersign(buildBlockServerPayload(userID, blockedUserID, h.signingKey.Fingerprint, userSignature, now), now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing block countersignature")
		internalServerError(w)
		return
	}
	cert := BlockCert{
		Type:            identityTypeBlock,
		UserID:          userID,
		BlockedUserID:   blockedUserID,
		UserSignature:   UserSignature{ID: keyID, Armor: userSignature},
		ServerSignature: serverSignature,
	}
	created, dropped, err := h.services.db.InsertBlock(r.Context(), cert)
	if err != nil {
		if errors.Is(err, ErrBlockConflict) {
			stored, getErr := h.services.db.GetBlock(r.Context(), userID, blockedUserID)
			if getErr == nil && stored != nil && stored.UserSignature.Armor == userSignature {
				writeResponse(w, http.StatusOK, stored)
				return
			}
			writeResponse(w, http.StatusConflict, "Block already exists with a different signature")
			return
		}
		log.Error().Err(err).Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Error storing block")
		internalServerError(w)
		return
	}
	if created {
		h.afterBlock(cert, dropped)
	}

	log.Info().Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Block accepted")
	writeResponse(w, http.StatusOK, cert)
}

// afterBlock runs a new block's side effects outside its transaction.
func (h *Handlers) afterBlock(cert BlockCert, dropped []string) {
	if h.realtimeRelay == nil {
		return
	}
	for _, reedID := range dropped {
		h.realtimeRelay.notifyReedCoverage(reedID)
	}
}

// UnblockUser handles DELETE /users/{userID}/block: an unsigned retraction
// of the caller's block. 204 whether or not a block existed.
func (h *Handlers) UnblockUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	blockedUserID := strings.TrimSpace(mux.Vars(r)["userID"])

	deleted, err := h.services.db.DeleteBlock(r.Context(), userID, blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Error deleting block")
		internalServerError(w)
		return
	}
	if deleted {
		log.Info().Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Block lifted")
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListMyBlocks handles GET /blocks: the caller's own blocks.
func (h *Handlers) ListMyBlocks(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	blocks, err := h.services.db.ListBlocksByUser(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Error listing blocks")
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, map[string]any{"blocks": blocks})
}
