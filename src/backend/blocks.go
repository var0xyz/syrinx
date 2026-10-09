//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
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

// oweBlockEventTx owes this block or lift to the blocked user's client
// when they are local, or to their home server when not. Whatever was
// still owed for the pair is replaced: only the latest state matters.
func (s *DataService) oweBlockEventTx(ctx context.Context, tx *sql.Tx, userID, blockedUserID, kind string) error {
	_, blockedServerID, ok := parseIdentityID(identityID(blockedUserID))
	if !ok {
		return nil
	}
	var err error
	if blockedServerID == s.serverID {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO user_block_events (user_id, blocked_user_id, kind)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, blocked_user_id) DO UPDATE SET kind = EXCLUDED.kind
		`, userID, blockedUserID, kind)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO block_peer_notices (server_id, user_id, blocked_user_id, kind)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (server_id, user_id, blocked_user_id) DO UPDATE SET kind = EXCLUDED.kind
		`, blockedServerID, userID, blockedUserID, kind)
	}
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

// BlockNoticesOwedTo returns every block and lift serverID hasn't accepted.
func (s *DataService) BlockNoticesOwedTo(ctx context.Context, serverID string) ([]blockEvent, error) {
	return scanBlockEvents(s.db.QueryContext(ctx, `
		SELECT user_id, blocked_user_id, kind FROM block_peer_notices WHERE server_id = $1
	`, serverID))
}

// DeleteBlockNotice drops a notice the peer accepted, unless a later one of
// another kind replaced it meanwhile.
func (s *DataService) DeleteBlockNotice(ctx context.Context, serverID string, e blockEvent) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM block_peer_notices
		WHERE server_id = $1 AND user_id = $2 AND blocked_user_id = $3 AND kind = $4
	`, serverID, e.UserID, e.BlockedUserID, e.Kind)
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
	writeResponse(w, http.StatusForbidden, &pb.Error{
		Message: "Blocked",
		Detail:  &pb.Error_Block{Block: pbBlockCert(cert)},
	})
}

// refuseIfBlocked answers 403 + cert when authorID blocked the caller. A
// removed account is left to answer 410 instead.
func (h *Handlers) refuseIfBlocked(w http.ResponseWriter, r *http.Request, authorID string) bool {
	viewerID := h.requestViewer(r)
	if viewerID == "" || viewerID == authorID {
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

// refuseBlockedRequester answers a peer leg with 403 + cert when authorID
// blocked the peer's requester.
func (h *Handlers) refuseBlockedRequester(w http.ResponseWriter, r *http.Request, authorID, requesterID string) bool {
	cert, err := h.services.db.GetBlock(r.Context(), authorID, requesterID)
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("authorID", authorID).Msg("Error loading block")
		internalServerError(w)
		return true
	}
	if cert == nil {
		return false
	}
	writeBlocked(w, cert)
	return true
}

// requestViewer is who a read is for: the caller's session user, or the
// user a peer names in `requester` on a proxied read.
func (h *Handlers) requestViewer(r *http.Request) string {
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		return userID
	}
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok {
		return ""
	}
	requester := strings.TrimSpace(r.URL.Query().Get("requester"))
	if _, serverID, ok := parseIdentityID(identityID(requester)); !ok || serverID != peerServerID {
		return ""
	}
	return requester
}

// dropMentionsBlockingAuthor leaves out mentioned users who blocked the
// author: the reed stays as signed, they just aren't sent the mention.
func (h *Handlers) dropMentionsBlockingAuthor(ctx context.Context, mentions []string, authorID string) ([]string, error) {
	blocking, err := h.services.db.UsersBlocking(ctx, authorID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(mentions, func(id string) bool { return blocking[id] }), nil
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
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	blockedUserID := strings.TrimSpace(mux.Vars(r)["userID"])
	if _, _, ok := parseIdentityID(identityID(blockedUserID)); !ok {
		writeError(w, http.StatusBadRequest, "Argument `userID` is invalid")
		return
	}
	if blockedUserID == userID {
		writeError(w, http.StatusBadRequest, "Cannot block yourself")
		return
	}
	blockedServerID, foreign := h.foreignServerOf(blockedUserID)

	req := &pb.BlockRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature := strings.TrimSpace(req.GetSignature())
	bareFingerprint := strings.TrimSpace(req.GetFingerprint())
	if userSignature == "" || bareFingerprint == "" {
		writeError(w, http.StatusBadRequest, "Arguments `signature` and `fingerprint` are required")
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
		writeAccountGone(w, h.accountRemovalWire(removal))
		return
	}
	if foreign {
		peer, err := h.services.db.GetServerByID(r.Context(), blockedServerID)
		if err != nil {
			internalServerError(w)
			return
		}
		if peer == nil {
			writeError(w, http.StatusNotFound, "User not found")
			return
		}
		if err := h.services.db.UpsertRemoteIdentity(r.Context(), blockedUserID, blockedServerID); err != nil {
			log.Error().Err(err).Str("blockedUserID", blockedUserID).Msg("Error recording remote identity")
			internalServerError(w)
			return
		}
	} else {
		profile, err := h.services.db.GetUserProfile(r.Context(), blockedUserID)
		if err != nil {
			log.Error().Err(err).Str("blockedUserID", blockedUserID).Msg("Error loading blocked user")
			internalServerError(w)
			return
		}
		if profile == nil {
			writeError(w, http.StatusNotFound, "User not found")
			return
		}
	}

	existing, err := h.services.db.GetBlock(r.Context(), userID, blockedUserID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Error loading block")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature.Armor != userSignature {
			writeError(w, http.StatusConflict, "Block already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, pbBlockCert(existing))
		return
	}

	pubKey, err := h.resolvePublicKey(r.Context(), keyID)
	if err != nil {
		log.Error().Err(err).Str("keyID", keyID).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(buildBlockUserPayload(userID, blockedUserID, keyID)), userSignature, pubKey.Armor); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
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
				writeResponse(w, http.StatusOK, pbBlockCert(stored))
				return
			}
			writeError(w, http.StatusConflict, "Block already exists with a different signature")
			return
		}
		log.Error().Err(err).Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Error storing block")
		internalServerError(w)
		return
	}
	if created {
		h.afterBlock(cert, dropped)
		h.sendBlockNoticesFor(blockedUserID)
	}

	log.Info().Str("userID", userID).Str("blockedUserID", blockedUserID).Msg("Block accepted")
	writeResponse(w, http.StatusOK, pbBlockCert(&cert))
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

// afterUnblock tells the blocked user, or their home server, of a lift.
func (h *Handlers) afterUnblock(userID, blockedUserID string) {
	if h.realtimeRelay != nil {
		h.realtimeRelay.pushUnblock(userID, blockedUserID)
	}
	h.sendBlockNoticesFor(blockedUserID)
}

// sendBlockNoticesFor delivers what blockedUserID's home server is owed, when
// that server is a peer.
func (h *Handlers) sendBlockNoticesFor(blockedUserID string) {
	if peerID, foreign := h.foreignServerOf(blockedUserID); foreign {
		go h.sendOwedBlockNotices(peerID)
	}
}

// UnblockUser handles DELETE /users/{userID}/block: an unsigned retraction
// of the caller's block. 204 whether or not a block existed.
func (h *Handlers) UnblockUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
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
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	blocks, err := h.services.db.ListBlocksByUser(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Error listing blocks")
		internalServerError(w)
		return
	}
	out := &pb.BlockListResponse{}
	for i := range blocks {
		out.Blocks = append(out.Blocks, pbBlockCert(&blocks[i]))
	}
	writeResponse(w, http.StatusOK, out)
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

// blockNoticeLocks serializes sends to each peer, so a block and its lift
// for the same pair can't cross on the wire.
var blockNoticeLocks sync.Map

// sendOwedBlockNotices sends peerID every block and lift it hasn't accepted.
func (h *Handlers) sendOwedBlockNotices(peerID string) {
	lock, _ := blockNoticeLocks.LoadOrStore(peerID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := h.services.log.GetLogger(ctx)

	notices, err := h.services.db.BlockNoticesOwedTo(ctx, peerID)
	if err != nil {
		log.Error().Err(err).Str("peerServerID", peerID).Msg("Failed to list block notices owed to peer")
		return
	}
	if len(notices) == 0 {
		return
	}
	peer, err := h.services.db.GetServerByID(ctx, peerID)
	if err != nil || peer == nil {
		return
	}
	for _, n := range notices {
		var path string
		var body proto.Message
		if n.Kind == blockEventBlock {
			cert, err := h.services.db.GetBlock(ctx, n.UserID, n.BlockedUserID)
			if err != nil || cert == nil {
				log.Error().Err(err).Str("userID", n.UserID).Str("blockedUserID", n.BlockedUserID).Msg("Failed to load owed block")
				continue
			}
			path, body = "/api/federation/relay/block-notify", pbBlockCert(cert)
		} else {
			path, body = "/api/federation/relay/unblock-notify", &pb.RelayUnblockPayload{
				UserId: n.UserID, BlockedUserId: n.BlockedUserID,
			}
		}
		status, err := h.callPeerRelayEndpoint(ctx, peerID, peer.BaseURL, path, body, nil)
		if err != nil || status < 200 || status >= 300 {
			log.Warn().Err(err).Int("status", status).Str("peerServerID", peerID).Str("kind", n.Kind).Msg("Peer did not accept block notice")
			continue
		}
		if err := h.services.db.DeleteBlockNotice(ctx, peerID, n); err != nil {
			log.Error().Err(err).Str("peerServerID", peerID).Msg("Failed to clear block notice")
		}
	}
}

// notifyPeersOfOwedBlockNotices retries, at boot, every block notice a
// connected peer hasn't accepted yet.
func (h *Handlers) notifyPeersOfOwedBlockNotices() {
	peers, err := h.services.db.ListConnectedPeers(context.Background())
	if err != nil {
		h.services.log.GetLogger(context.Background()).Error().Err(err).Msg("Failed to list peers for owed block notices")
		return
	}
	for _, peer := range peers {
		h.sendOwedBlockNotices(peer.ID)
	}
}

// acceptForeignBlock verifies a block made on peerServerID against one of
// this server's users, stores it and applies it. Idempotent; a newer
// block replaces an older one, an older one is ignored.
func (h *Handlers) acceptForeignBlock(ctx context.Context, peerServerID string, cert BlockCert) error {
	_, userServerID, ok := parseIdentityID(identityID(cert.UserID))
	if cert.Type != identityTypeBlock || !ok || userServerID != peerServerID {
		return fmt.Errorf("blocking user is not a user of the calling peer")
	}
	if _, blockedServerID, ok := parseIdentityID(identityID(cert.BlockedUserID)); !ok || blockedServerID != h.services.db.GetServerID() {
		return fmt.Errorf("blocked user is not local")
	}
	if !requesterKeyBelongsTo(cert.UserSignature.ID, cert.UserID, peerServerID) {
		return fmt.Errorf("signing key is not the blocking user's")
	}
	key, err := h.resolvePublicKey(ctx, cert.UserSignature.ID)
	if err != nil || key == nil {
		return fmt.Errorf("resolve blocking user key: %v", err)
	}
	userPayload := buildBlockUserPayload(cert.UserID, cert.BlockedUserID, cert.UserSignature.ID)
	if err := h.services.crypto.verifySignature(string(userPayload), cert.UserSignature.Armor, key.Armor); err != nil {
		return fmt.Errorf("user signature: %w", err)
	}
	signedAt := cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	if err := h.verifyPeerCountersignature(ctx, peerServerID, cert.ServerSignature, func(serverFP string) []byte {
		return buildBlockServerPayload(cert.UserID, cert.BlockedUserID, serverFP, cert.UserSignature.Armor, signedAt)
	}); err != nil {
		return err
	}
	if err := h.services.db.UpsertRemoteIdentity(ctx, cert.UserID, peerServerID); err != nil {
		return err
	}

	stored, err := h.services.db.GetBlock(ctx, cert.UserID, cert.BlockedUserID)
	if err != nil {
		return err
	}
	if stored != nil && !signedAt.After(stored.ServerSignature.SignedAt) {
		return nil
	}
	if stored != nil {
		if _, err := h.services.db.DeleteBlock(ctx, cert.UserID, cert.BlockedUserID); err != nil {
			return err
		}
	}
	created, dropped, err := h.services.db.InsertBlock(ctx, cert)
	if err != nil {
		return err
	}
	if created {
		h.afterBlock(cert, dropped)
	}
	return nil
}

// acceptRefusalBlock stores the block a peer refused a request with, so
// later requests are refused here without asking it.
func (h *Handlers) acceptRefusalBlock(ctx context.Context, peerServerID string, body []byte) {
	var refusal pb.Error
	if err := proto.Unmarshal(body, &refusal); err != nil || refusal.GetBlock() == nil {
		return
	}
	if err := h.acceptForeignBlock(ctx, peerServerID, blockCertFromPB(refusal.GetBlock())); err != nil {
		h.services.log.GetLogger(ctx).Warn().Err(err).Str("peerServerID", peerServerID).Msg("Rejected block a peer refused a request with")
	}
}

// BlockNotifyFromPeer receives a block one of the calling peer's users made
// against one of this server's users.
func (h *Handlers) BlockNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var msg pb.BlockCert
	if err := readRequest(r, &msg); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "block-notify", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	cert := blockCertFromPB(&msg)
	if err := h.acceptForeignBlock(r.Context(), peerServerID, cert); err != nil {
		log.Warn().Err(err).Str("peerServerID", peerServerID).Str("userID", cert.UserID).Msg("Rejected block from peer")
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "block-notify", false)
		writeError(w, http.StatusBadRequest, "Block failed verification")
		return
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "block-notify", true)
	w.WriteHeader(http.StatusNoContent)
}

// UnblockNotifyFromPeer lifts a block the calling peer's user made.
func (h *Handlers) UnblockNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var req pb.RelayUnblockPayload
	if err := readRequest(r, &req); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unblock-notify", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if _, serverID, ok := parseIdentityID(identityID(req.UserId)); !ok || serverID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unblock-notify", false)
		writeError(w, http.StatusBadRequest, "user_id does not belong to the calling peer")
		return
	}

	deleted, err := h.services.db.DeleteBlock(r.Context(), req.UserId, req.BlockedUserId)
	if err != nil {
		log.Error().Err(err).Str("userID", req.UserId).Msg("Error deleting block")
		internalServerError(w)
		return
	}
	if deleted && h.realtimeRelay != nil {
		h.realtimeRelay.pushUnblock(req.UserId, req.BlockedUserId)
	}
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "unblock-notify", true)
	w.WriteHeader(http.StatusNoContent)
}
