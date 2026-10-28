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
	"github.com/lib/pq"
	"github.com/rs/zerolog/log"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// FollowCert is a follow, or with Follow false an unfollow, signed by the
// follower and countersigned by their home server.
type FollowCert struct {
	FollowerID      string
	FolloweeID      string
	Follow          bool
	UserSignature   UserSignature
	ServerSignature ServerSignature
}

func pbFollowCert(c *FollowCert) *pb.FollowCert {
	return &pb.FollowCert{
		FollowerId:      c.FollowerID,
		FolloweeId:      c.FolloweeID,
		Follow:          c.Follow,
		UserSignature:   pbUserSignature(c.UserSignature),
		ServerSignature: pbServerSignature(c.ServerSignature),
	}
}

func followCertFromPB(m *pb.FollowCert) FollowCert {
	return FollowCert{
		FollowerID:      m.GetFollowerId(),
		FolloweeID:      m.GetFolloweeId(),
		Follow:          m.GetFollow(),
		UserSignature:   UserSignature{ID: m.GetUserSignature().GetId(), Armor: m.GetUserSignature().GetArmor()},
		ServerSignature: serverSignatureFromPB(m.GetServerSignature()),
	}
}

const followCertSelect = `
	SELECT f.follower_id, f.followee_id, f.follow,
		us.public_key_id, us.signature,
		ss.private_key_id, ss.signature, ss.signed_at
	FROM follow_certs f
	JOIN user_signatures us ON us.id = f.user_signature_id
	JOIN server_signatures ss ON ss.id = f.server_signature_id`

func scanFollowCert(row blockRowScanner) (*FollowCert, error) {
	var c FollowCert
	err := row.Scan(&c.FollowerID, &c.FolloweeID, &c.Follow,
		&c.UserSignature.ID, &c.UserSignature.Armor,
		&c.ServerSignature.ID, &c.ServerSignature.Armor, &c.ServerSignature.SignedAt)
	if err != nil {
		return nil, err
	}
	c.ServerSignature.SignedAt = c.ServerSignature.SignedAt.UTC()
	return &c, nil
}

func scanFollowCerts(rows *sql.Rows, err error) ([]FollowCert, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []FollowCert
	for rows.Next() {
		c, err := scanFollowCert(rows)
		if err != nil {
			return nil, err
		}
		certs = append(certs, *c)
	}
	return certs, rows.Err()
}

// GetFollowCert returns the certificate held for the pair, or nil.
func (s *DataService) GetFollowCert(ctx context.Context, followerID, followeeID string) (*FollowCert, error) {
	c, err := scanFollowCert(s.db.QueryRowContext(ctx,
		followCertSelect+` WHERE f.follower_id = $1 AND f.followee_id = $2`, followerID, followeeID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// ActiveFollowCerts returns userID's active follows, and the active follows
// of userID.
func (s *DataService) ActiveFollowCerts(ctx context.Context, userID string) (follows, followers []FollowCert, err error) {
	if follows, err = scanFollowCerts(s.db.QueryContext(ctx, followCertSelect+` WHERE f.follower_id = $1 AND f.follow`, userID)); err != nil {
		return nil, nil, err
	}
	followers, err = scanFollowCerts(s.db.QueryContext(ctx, followCertSelect+` WHERE f.followee_id = $1 AND f.follow`, userID))
	return follows, followers, err
}

// followCertsOf returns followeeID's active follows by followerIDs.
func (s *DataService) followCertsOf(ctx context.Context, followeeID string, followerIDs []string) (map[string]FollowCert, error) {
	certs, err := scanFollowCerts(s.db.QueryContext(ctx,
		followCertSelect+` WHERE f.followee_id = $1 AND f.follower_id = ANY($2) AND f.follow`,
		followeeID, pq.Array(followerIDs)))
	if err != nil {
		return nil, err
	}
	byFollower := make(map[string]FollowCert, len(certs))
	for _, c := range certs {
		byFollower[c.FollowerID] = c
	}
	return byFollower, nil
}

// SaveFollowCert makes cert the pair's state unless a newer one is held:
// it sets the edges this server keeps and, with owe, owes cert to the
// followee's client or home server. applied is false when it was older.
func (s *DataService) SaveFollowCert(ctx context.Context, cert FollowCert, owe bool) (applied bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if applied, err = s.saveFollowCertTx(ctx, tx, cert, owe); err != nil || !applied {
		return false, err
	}
	return true, tx.Commit()
}

func (s *DataService) saveFollowCertTx(ctx context.Context, tx *sql.Tx, cert FollowCert, owe bool) (bool, error) {
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	var oldUserSigID, oldServerSigID int64
	var heldAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT f.user_signature_id, f.server_signature_id, ss.signed_at
		FROM follow_certs f JOIN server_signatures ss ON ss.id = f.server_signature_id
		WHERE f.follower_id = $1 AND f.followee_id = $2
		FOR UPDATE OF f
	`, cert.FollowerID, cert.FolloweeID).Scan(&oldUserSigID, &oldServerSigID, &heldAt)
	held := err == nil
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if held && !cert.ServerSignature.SignedAt.After(heldAt) {
		return false, nil
	}

	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, cert.UserSignature, cert.ServerSignature)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO follow_certs (follower_id, followee_id, follow, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (follower_id, followee_id) DO UPDATE SET
			follow = EXCLUDED.follow,
			user_signature_id = EXCLUDED.user_signature_id,
			server_signature_id = EXCLUDED.server_signature_id
	`, cert.FollowerID, cert.FolloweeID, cert.Follow, userSigID, serverSigID); err != nil {
		return false, fmt.Errorf("store follow cert: %w", err)
	}
	if held {
		if _, err := tx.ExecContext(ctx, `
			WITH us AS (DELETE FROM user_signatures WHERE id = $1)
			DELETE FROM server_signatures WHERE id = $2
		`, oldUserSigID, oldServerSigID); err != nil {
			return false, fmt.Errorf("delete superseded follow signatures: %w", err)
		}
	}
	if err := s.setFollowEdgesTx(ctx, tx, cert.FollowerID, cert.FolloweeID, cert.Follow); err != nil {
		return false, err
	}
	if owe {
		if err := s.oweFollowCertTx(ctx, tx, cert.FollowerID, cert.FolloweeID); err != nil {
			return false, err
		}
	}
	return true, dropSettledUnfollowTx(ctx, tx, cert.FollowerID, cert.FolloweeID)
}

// setFollowEdgesTx adds or removes the edges this server keeps for the
// pair: user_following for a local follower, user_followers for a local
// followee.
func (s *DataService) setFollowEdgesTx(ctx context.Context, tx *sql.Tx, followerID, followeeID string, follow bool) error {
	local := func(id string) bool {
		_, serverID, ok := parseIdentityID(identityID(id))
		return ok && serverID == s.serverID
	}
	type edge struct{ table, ownerCol, otherCol, owner, other string }
	var edges []edge
	if local(followerID) {
		edges = append(edges, edge{"user_following", "user_id", "following_user_id", followerID, followeeID})
	}
	if local(followeeID) {
		edges = append(edges, edge{"user_followers", "user_id", "follower_user_id", followeeID, followerID})
	}
	for _, e := range edges {
		q := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND %s = $2`, e.table, e.ownerCol, e.otherCol)
		if follow {
			q = fmt.Sprintf(`INSERT INTO %s (%s, %s) VALUES ($1, $2) ON CONFLICT DO NOTHING`, e.table, e.ownerCol, e.otherCol)
		}
		if _, err := tx.ExecContext(ctx, q, e.owner, e.other); err != nil {
			return fmt.Errorf("set %s: %w", e.table, err)
		}
	}
	return nil
}

// oweFollowCertTx owes the pair's certificate to the followee: their
// client when they are local, their home server otherwise.
func (s *DataService) oweFollowCertTx(ctx context.Context, tx *sql.Tx, followerID, followeeID string) error {
	if _, serverID, ok := parseIdentityID(identityID(followeeID)); ok && serverID != s.serverID {
		return enqueuePeerEventForTx(ctx, tx, serverID, peerEventFollow, followerID, followeeID)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO follow_event_queue (follower_id, followee_id) VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, followerID, followeeID)
	if err != nil {
		return fmt.Errorf("owe follow cert: %w", err)
	}
	return nil
}

// dropSettledUnfollowTx deletes an unfollow nobody is owed anymore: the
// server keeps only active follows.
func dropSettledUnfollowTx(ctx context.Context, tx *sql.Tx, followerID, followeeID string) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM follow_certs f
		WHERE f.follower_id = $1 AND f.followee_id = $2 AND NOT f.follow
			AND NOT EXISTS (SELECT 1 FROM follow_event_queue n WHERE n.follower_id = f.follower_id AND n.followee_id = f.followee_id)
			AND NOT EXISTS (SELECT 1 FROM peer_event_queue e
				WHERE e.kind = 'follow' AND e.subject_id = f.follower_id AND e.object_id = f.followee_id)
	`, followerID, followeeID)
	return err
}

// DropSettledUnfollow is dropSettledUnfollowTx on its own, once a peer has
// the pair's certificate.
func (s *DataService) DropSettledUnfollow(ctx context.Context, followerID, followeeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := dropSettledUnfollowTx(ctx, tx, followerID, followeeID); err != nil {
		return err
	}
	return tx.Commit()
}

// UndoRefusedFollow undoes a follow the followee's server refused, unless
// a newer certificate than the one it refused replaced it meanwhile.
func (s *DataService) UndoRefusedFollow(ctx context.Context, followerID, followeeID string, signedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT ss.signed_at FROM follow_certs f JOIN server_signatures ss ON ss.id = f.server_signature_id
		WHERE f.follower_id = $1 AND f.followee_id = $2 AND f.follow
		FOR UPDATE OF f
	`, followerID, followeeID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.UTC().Equal(signedAt.UTC().Truncate(time.Second)) {
		return nil
	}
	if err := s.setFollowEdgesTx(ctx, tx, followerID, followeeID, false); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM follow_certs WHERE follower_id = $1 AND followee_id = $2`, followerID, followeeID); err != nil {
		return err
	}
	return tx.Commit()
}

// FollowEventsFor returns the certificates followeeID's client is owed,
// oldest first.
func (s *DataService) FollowEventsFor(ctx context.Context, followeeID string) ([]FollowCert, error) {
	return scanFollowCerts(s.db.QueryContext(ctx, followCertSelect+`
		JOIN follow_event_queue n ON n.follower_id = f.follower_id AND n.followee_id = f.followee_id
		WHERE n.followee_id = $1 ORDER BY ss.signed_at`, followeeID))
}

// AckFollowEvent drops what the client acked, unless a certificate of the
// other kind replaced it meanwhile.
func (s *DataService) AckFollowEvent(ctx context.Context, followerID, followeeID string, follow bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM follow_event_queue n USING follow_certs f
		WHERE n.follower_id = $1 AND n.followee_id = $2
			AND f.follower_id = n.follower_id AND f.followee_id = n.followee_id AND f.follow = $3
	`, followerID, followeeID, follow); err != nil {
		return err
	}
	if err := dropSettledUnfollowTx(ctx, tx, followerID, followeeID); err != nil {
		return err
	}
	return tx.Commit()
}

// FollowUser handles POST /users/{userID}/follow.
func (h *Handlers) FollowUser(w http.ResponseWriter, r *http.Request) { h.signFollow(w, r, true) }

// UnfollowUser handles DELETE /users/{userID}/follow.
func (h *Handlers) UnfollowUser(w http.ResponseWriter, r *http.Request) { h.signFollow(w, r, false) }

// signFollow countersigns the caller's signed follow or unfollow of
// userID, applies it here and delivers it to the followee.
func (h *Handlers) signFollow(w http.ResponseWriter, r *http.Request, follow bool) {
	log := h.services.log.GetLogger(r.Context())
	followerID, ok := r.Context().Value(userIDKey).(string)
	if !ok || followerID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	followeeID := strings.TrimSpace(mux.Vars(r)["userID"])
	if _, _, ok := parseIdentityID(identityID(followeeID)); !ok {
		writeError(w, http.StatusBadRequest, "Argument `userID` is invalid")
		return
	}
	if followeeID == followerID {
		writeError(w, http.StatusBadRequest, "Cannot follow yourself")
		return
	}
	req := &pb.FollowRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature, fingerprint := strings.TrimSpace(req.GetSignature()), strings.TrimSpace(req.GetFingerprint())
	if userSignature == "" || fingerprint == "" {
		writeError(w, http.StatusBadRequest, "Arguments `signature` and `fingerprint` are required")
		return
	}
	peerID, foreign := h.foreignServerOf(followeeID)

	if follow {
		if refused := h.refuseFollowTarget(w, r, followeeID, foreign); refused {
			return
		}
	}
	var peerBaseURL string
	if foreign {
		peer, err := h.services.db.GetServerByID(r.Context(), peerID)
		if err != nil {
			internalServerError(w)
			return
		}
		if peer == nil {
			writeError(w, http.StatusNotFound, "User not found")
			return
		}
		peerBaseURL = peer.BaseURL
		if err := h.services.db.UpsertRemoteIdentity(r.Context(), followeeID, peerID); err != nil {
			log.Error().Err(err).Str("followeeID", followeeID).Msg("Error recording remote identity")
			internalServerError(w)
			return
		}
	}

	keyID := string(appendEntity(identityID(followerID), fingerprint))
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
	if err := h.services.crypto.verifySignature(r.Context(), string(buildFollowUserPayload(followerID, followeeID, keyID, follow)), userSignature, pubKey.Armor); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	serverSignature, err := h.countersign(r.Context(), buildFollowServerPayload(followerID, followeeID, follow, h.signingKey.Fingerprint, userSignature, now), now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing follow countersignature")
		internalServerError(w)
		return
	}
	cert := FollowCert{
		FollowerID:      followerID,
		FolloweeID:      followeeID,
		Follow:          follow,
		UserSignature:   UserSignature{ID: keyID, Armor: userSignature},
		ServerSignature: serverSignature,
	}

	// A follow the peer answers now gets its refusal back to the follower;
	// one it can't answer is owed and sent again later.
	owe := true
	if foreign && follow {
		status, body, err := h.sendFollowNotice(r.Context(), peerID, peerBaseURL, cert)
		switch {
		case err == nil && status >= 200 && status < 300:
			owe = false
		case err == nil && status >= 400 && status < 500 && status != http.StatusTooManyRequests:
			w.Header().Set("Content-Type", protobufContentType)
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
	}
	if _, err := h.services.db.SaveFollowCert(r.Context(), cert, owe); err != nil {
		log.Error().Err(err).Str("followerID", followerID).Str("followeeID", followeeID).Msg("Error storing follow")
		internalServerError(w)
		return
	}
	if foreign && owe {
		h.drainPeers(peerID)
	} else if !foreign && h.realtimeRelay != nil {
		h.realtimeRelay.pushFollow(&cert)
	}

	op := metrics.OpAdd
	if !follow {
		op = metrics.OpRemove
	}
	h.metrics.Follow(r.Context(), op, foreign)
	writeResponse(w, http.StatusOK, pbFollowCert(&cert))
}

// refuseFollowTarget answers a follow of a local followeeID that can't be
// followed: gone, removed, blocking the caller, or not claimed yet.
func (h *Handlers) refuseFollowTarget(w http.ResponseWriter, r *http.Request, followeeID string, foreign bool) bool {
	if foreign {
		return false
	}
	if h.refuseIfBlocked(w, r, followeeID) {
		return true
	}
	profile, err := h.services.db.GetUserProfile(r.Context(), followeeID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return true
	}
	if unclaimed, err := h.services.db.IsUnclaimed(r.Context(), followeeID); err != nil {
		internalServerError(w)
		return true
	} else if unclaimed {
		writeError(w, http.StatusConflict, "This account's owner hasn't come back to this server yet")
		return true
	}
	return false
}

func newFollowedMsg(cert *FollowCert) *pb.WSMessage {
	return &pb.WSMessage{
		Type:    pb.MessageType_FOLLOWED,
		Payload: &pb.WSMessage_Followed{Followed: &pb.FollowedMessage{Cert: pbFollowCert(cert)}},
	}
}

// pushFollow tells an online followee of a follow or unfollow. Offline,
// it waits for their next SYNC_REQUEST.
func (rs *realtimeService) pushFollow(cert *FollowCert) {
	_ = rs.connManager.SendToUser(cert.FolloweeID, newFollowedMsg(cert))
}

// catchUpFollows sends followeeID every certificate they haven't acked.
func (rs *realtimeService) catchUpFollows(ctx context.Context, followeeID string) {
	certs, err := rs.db.FollowEventsFor(ctx, followeeID)
	if err != nil {
		log.Error().Err(err).Str("followeeID", followeeID).Msg("Failed to load owed follows")
		return
	}
	for i := range certs {
		rs.connManager.SendToUser(followeeID, newFollowedMsg(&certs[i]))
	}
}

// ackFollowEvent drops the certificate a client acked.
func (rs *realtimeService) ackFollowEvent(ctx context.Context, client *realtimeClient, ack *pb.FollowedAckMessage) {
	if ack.GetFollowerId() == "" {
		return
	}
	if err := rs.db.AckFollowEvent(ctx, ack.GetFollowerId(), client.userID, ack.GetFollow()); err != nil {
		log.Error().Err(err).Str("followerID", ack.GetFollowerId()).Str("followeeID", client.userID).Msg("Failed to drop acked follow")
	}
}

// sendFollowNotice delivers cert to the followee's home server, returning
// its status and body.
func (h *Handlers) sendFollowNotice(ctx context.Context, peerID, baseURL string, cert FollowCert) (int, []byte, error) {
	return h.callPeerRelayEndpointRaw(ctx, peerID, baseURL, "/api/federation/relay/follow-notify", pbFollowCert(&cert))
}

// errFollowRefused marks a peer's follow that failed verification.
var errFollowRefused = errors.New("follow refused")

// verifyForeignFollow checks a follow made on peerServerID of one of this
// server's users: signed by a key of the follower's, countersigned by the
// peer.
func (h *Handlers) verifyForeignFollow(ctx context.Context, peerServerID string, cert FollowCert) error {
	if _, serverID, ok := parseIdentityID(identityID(cert.FollowerID)); !ok || serverID != peerServerID {
		return fmt.Errorf("%w: follower is not a user of the calling peer", errFollowRefused)
	}
	if _, serverID, ok := parseIdentityID(identityID(cert.FolloweeID)); !ok || serverID != h.services.db.GetServerID() {
		return fmt.Errorf("%w: followee is not local", errFollowRefused)
	}
	if !requesterKeyBelongsTo(cert.UserSignature.ID, cert.FollowerID, peerServerID) {
		return fmt.Errorf("%w: signing key is not the follower's", errFollowRefused)
	}
	key, err := h.resolvePublicKey(ctx, cert.UserSignature.ID)
	if err != nil {
		return fmt.Errorf("resolve follower key: %w", err)
	}
	if key == nil {
		return fmt.Errorf("%w: follower key could not be resolved", errFollowRefused)
	}
	userPayload := buildFollowUserPayload(cert.FollowerID, cert.FolloweeID, cert.UserSignature.ID, cert.Follow)
	if err := h.services.crypto.verifySignature(ctx, string(userPayload), cert.UserSignature.Armor, key.Armor); err != nil {
		return fmt.Errorf("%w: user signature: %v", errFollowRefused, err)
	}
	signedAt := cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	if err := h.verifyPeerCountersignature(ctx, peerServerID, cert.ServerSignature, func(serverFP string) []byte {
		return buildFollowServerPayload(cert.FollowerID, cert.FolloweeID, cert.Follow, serverFP, cert.UserSignature.Armor, signedAt)
	}); err != nil {
		return fmt.Errorf("%w: %v", errFollowRefused, err)
	}
	return nil
}

// FollowNotifyFromPeer receives a follow or unfollow one of the calling
// peer's users made of one of this server's users.
func (h *Handlers) FollowNotifyFromPeer(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	accepted := false
	defer func() { h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "follow-notify", accepted) }()
	var msg pb.FollowCert
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	cert := followCertFromPB(&msg)
	if err := h.verifyForeignFollow(r.Context(), peerServerID, cert); err != nil {
		if errors.Is(err, errFollowRefused) {
			log.Warn().Err(err).Str("peerServerID", peerServerID).Str("followerID", cert.FollowerID).Msg("Rejected follow from peer")
			writeError(w, http.StatusBadRequest, "Follow failed verification")
			return
		}
		log.Error().Err(err).Str("peerServerID", peerServerID).Msg("Failed to verify follow from peer")
		internalServerError(w)
		return
	}
	if cert.Follow {
		if h.refuseBlockedRequester(w, r, cert.FolloweeID, cert.FollowerID) || h.refuseFollowTarget(w, r, cert.FolloweeID, false) {
			return
		}
	}
	if err := h.services.db.UpsertRemoteIdentity(r.Context(), cert.FollowerID, peerServerID); err != nil {
		internalServerError(w)
		return
	}
	applied, err := h.services.db.SaveFollowCert(r.Context(), cert, true)
	if err != nil {
		log.Error().Err(err).Str("peerServerID", peerServerID).Str("followerID", cert.FollowerID).Msg("Failed to store follow from peer")
		internalServerError(w)
		return
	}
	if applied && h.realtimeRelay != nil {
		h.realtimeRelay.pushFollow(&cert)
	}
	accepted = true
	w.WriteHeader(http.StatusNoContent)
}
