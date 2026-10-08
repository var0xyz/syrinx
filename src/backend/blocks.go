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
	"github.com/rs/zerolog/log"

	pb "syrinx/proto"
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
	if err := s.oweBlockEventTx(ctx, tx, cert.UserID, cert.BlockedUserID, blockEventBlock); err != nil {
		return false, nil, err
	}
	return true, dropped, tx.Commit()
}

const (
	blockEventBlock   = "block"
	blockEventUnblock = "unblock"
)

// oweBlockEventTx owes this block or lift to the blocked user's client.
// Whatever was still owed for the pair is replaced: only the latest state
// matters.
func (s *DataService) oweBlockEventTx(ctx context.Context, tx *sql.Tx, userID, blockedUserID, kind string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_block_events (user_id, blocked_user_id, kind)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, blocked_user_id) DO UPDATE SET kind = EXCLUDED.kind
	`, userID, blockedUserID, kind)
	if err != nil {
		return fmt.Errorf("owe block event: %w", err)
	}
	return nil
}

// blockEvent is a block or lift still owed for a pair.
type blockEvent struct {
	UserID        string
	BlockedUserID string
	Kind          string
}

func scanBlockEvents(rows *sql.Rows, err error) ([]blockEvent, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []blockEvent
	for rows.Next() {
		var e blockEvent
		if err := rows.Scan(&e.UserID, &e.BlockedUserID, &e.Kind); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// BlockEventsFor returns the blocks and lifts userID's client hasn't acked.
func (s *DataService) BlockEventsFor(ctx context.Context, userID string) ([]blockEvent, error) {
	return scanBlockEvents(s.db.QueryContext(ctx, `
		SELECT user_id, blocked_user_id, kind FROM user_block_events WHERE blocked_user_id = $1
	`, userID))
}

// DeleteBlockEvent drops an event the client acked, unless a later one of
// another kind replaced it meanwhile.
func (s *DataService) DeleteBlockEvent(ctx context.Context, e blockEvent) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM user_block_events WHERE user_id = $1 AND blocked_user_id = $2 AND kind = $3
	`, e.UserID, e.BlockedUserID, e.Kind)
	return err
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_events pe
		USING pending_reed_events pre, reed_identities ri
		WHERE pre.event_id = pe.event_id AND ri.id = pre.reed_id
			AND pe.requester_user_id = $1 AND ri.author_id = $2
	`, blockedUserID, userID); err != nil {
		return nil, fmt.Errorf("drop blocked user's pending events: %w", err)
	}
	return dropped, nil
}

// UsersBlocking returns everyone who blocked blockedUserID.
func (s *DataService) UsersBlocking(ctx context.Context, blockedUserID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM user_blocks WHERE blocked_user_id = $1`, blockedUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		users[id] = true
	}
	return users, rows.Err()
}

// DeleteBlock removes userID's block of blockedUserID and owes the lift.
// deleted is false when there was none.
func (s *DataService) DeleteBlock(ctx context.Context, userID, blockedUserID string) (deleted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM user_blocks WHERE user_id = $1 AND blocked_user_id = $2
	`, userID, blockedUserID)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := s.oweBlockEventTx(ctx, tx, userID, blockedUserID, blockEventUnblock); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// writeBlocked answers a request the block refuses: 403 with the cert.
func writeBlocked(w http.ResponseWriter, cert *BlockCert) {
	writeResponse(w, http.StatusForbidden, cert)
}

// refuseIfBlocked answers 403 + cert when authorID blocked the caller. A
// removed account is left to answer 410 instead.
func (h *Handlers) refuseIfBlocked(w http.ResponseWriter, r *http.Request, authorID string) bool {
	viewerID, ok := r.Context().Value(userIDKey).(string)
	if !ok || viewerID == "" || viewerID == authorID {
		return false
	}
	log := h.services.log.GetLogger(r.Context())
	cert, err := h.services.db.GetBlock(r.Context(), authorID, viewerID)
	if err != nil {
		log.Error().Err(err).Str("authorID", authorID).Str("viewerID", viewerID).Msg("Error loading block")
		internalServerError(w)
		return true
	}
	if cert == nil {
		return false
	}
	removal, err := h.services.db.GetAccountRemoval(r.Context(), authorID)
	if err != nil {
		log.Error().Err(err).Str("authorID", authorID).Msg("Error loading account removal")
		internalServerError(w)
		return true
	}
	if removal != nil {
		return false
	}
	writeBlocked(w, cert)
	return true
}

// errRecipientBlocked: the content's author blocked the event's recipient.
var errRecipientBlocked = errors.New("recipient blocked by author")

func pbBlockCert(cert *BlockCert) *pb.BlockCert {
	return &pb.BlockCert{
		UserId:          cert.UserID,
		BlockedUserId:   cert.BlockedUserID,
		UserSignature:   pbUserSignature(cert.UserSignature),
		ServerSignature: pbServerSignature(cert.ServerSignature),
	}
}

func newUserBlockedMsg(requestID string, cert *BlockCert) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_USER_BLOCKED,
		Payload: &pb.WSMessage_UserBlocked{
			UserBlocked: &pb.UserBlockedMessage{RequestId: requestID, Block: pbBlockCert(cert)},
		},
	}
}

// blockedBy returns authorID's block of viewerID, nil when there is none or
// the lookup failed.
func (rs *realtimeService) blockedBy(authorID, viewerID string) *BlockCert {
	if authorID == "" || authorID == viewerID {
		return nil
	}
	cert, err := rs.db.GetBlock(context.Background(), authorID, viewerID)
	if err != nil {
		log.Error().Err(err).Str("authorID", authorID).Str("viewerID", viewerID).Msg("Failed to load block")
		return nil
	}
	return cert
}

// refuseIfBlocked answers a client's request for authorID's content with
// USER_BLOCKED when authorID blocked them.
func (rs *realtimeService) refuseIfBlocked(client *realtimeClient, requestID, authorID string) bool {
	cert := rs.blockedBy(authorID, client.userID)
	if cert == nil {
		return false
	}
	rs.connManager.SendToUser(client.userID, newUserBlockedMsg(requestID, cert))
	return true
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
	h.realtimeRelay.pushBlock(&cert)
}

// afterUnblock tells the blocked user of a lift.
func (h *Handlers) afterUnblock(userID, blockedUserID string) {
	if h.realtimeRelay != nil {
		h.realtimeRelay.pushUnblock(userID, blockedUserID)
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
		h.afterUnblock(userID, blockedUserID)
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

func newUserUnblockedMsg(userID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_USER_UNBLOCKED,
		Payload: &pb.WSMessage_UserUnblocked{
			UserUnblocked: &pb.UserUnblockedMessage{UserId: userID},
		},
	}
}

// pushBlock tells an online blocked user they were blocked. Offline, the
// event waits for their next SYNC_REQUEST.
func (rs *realtimeService) pushBlock(cert *BlockCert) {
	_ = rs.connManager.SendToUser(cert.BlockedUserID, newUserBlockedMsg("", cert))
}

// pushUnblock tells an online blocked user a block was lifted. Offline,
// the event waits for their next SYNC_REQUEST.
func (rs *realtimeService) pushUnblock(userID, blockedUserID string) {
	_ = rs.connManager.SendToUser(blockedUserID, newUserUnblockedMsg(userID))
}

// catchUpBlocks sends userID every block and lift they haven't acked.
func (rs *realtimeService) catchUpBlocks(blockedUserID string) {
	ctx := context.Background()
	events, err := rs.db.BlockEventsFor(ctx, blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("blockedUserID", blockedUserID).Msg("Failed to load owed block events")
		return
	}
	for _, e := range events {
		if e.Kind == blockEventUnblock {
			rs.connManager.SendToUser(blockedUserID, newUserUnblockedMsg(e.UserID))
			continue
		}
		cert := rs.blockedBy(e.UserID, blockedUserID)
		if cert == nil {
			continue
		}
		rs.connManager.SendToUser(blockedUserID, newUserBlockedMsg("", cert))
	}
}

// ackBlockEvent drops the event a client acked.
func (rs *realtimeService) ackBlockEvent(client *realtimeClient, userID, kind string) {
	if userID == "" {
		return
	}
	e := blockEvent{UserID: userID, BlockedUserID: client.userID, Kind: kind}
	if err := rs.db.DeleteBlockEvent(context.Background(), e); err != nil {
		log.Error().Err(err).Str("userID", userID).Str("userID", client.userID).Msg("Failed to drop acked block event")
	}
}
