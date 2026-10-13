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
	"unicode/utf8"

	"github.com/gorilla/mux"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// maxFlagNoteChars caps a flag's optional note.
const maxFlagNoteChars = 280

// flagClockSkew bounds how far a flag's signed createdAt may be from the
// server's clock.
const flagClockSkew = 5 * time.Minute

// ErrAlreadyFlagged: the reporter already flagged that reed.
var ErrAlreadyFlagged = errors.New("already flagged")

// reedFlag is a flag as stored and as it travels.
type reedFlag struct {
	ReedID     string
	AuthorID   string
	ReporterID string
	Note       string
	CreatedAt  time.Time
	Signature  UserSignature
}

func reedFlagFromPB(msg *pb.ReedFlag) reedFlag {
	return reedFlag{
		ReedID:     strings.TrimSpace(msg.GetReedId()),
		ReporterID: msg.GetReporterId(),
		Note:       msg.GetNote(),
		CreatedAt:  timeFromUnix(msg.GetCreatedAt()),
		Signature:  UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
	}
}

func pbReedFlag(f reedFlag) *pb.ReedFlag {
	return &pb.ReedFlag{
		ReedId:     f.ReedID,
		ReporterId: f.ReporterID,
		Note:       f.Note,
		CreatedAt:  unixOrZero(f.CreatedAt),
		Signature:  pbUserSignature(f.Signature),
	}
}

// checkFlag refuses a flag that isn't well formed: a reed ID, a short note,
// a fresh timestamp, and a key of the reporter's.
func checkFlag(f reedFlag) error {
	if _, ok := authorOf(identityID(f.ReedID)); !ok {
		return refuse(http.StatusBadRequest, "Invalid reed ID")
	}
	if utf8.RuneCountInString(f.Note) > maxFlagNoteChars {
		return refuse(http.StatusBadRequest, "A note can't exceed %d characters", maxFlagNoteChars)
	}
	if d := time.Since(f.CreatedAt); d > flagClockSkew || d < -flagClockSkew {
		return refuse(http.StatusBadRequest, "createdAt is too far from the server's clock")
	}
	owner, serverID, _, ok := parseKeyFingerprint(identityID(f.Signature.ID))
	if !ok || string(canonicalID(serverID, owner)) != f.ReporterID || f.Signature.Armor == "" {
		return refuse(http.StatusBadRequest, "The flag isn't signed by a key of the reporter's")
	}
	return nil
}

// StoreFlag stores a verified flag on a reed authored here. The reed's
// author is read from its identity row.
func (s *DataService) StoreFlag(ctx context.Context, f reedFlag) error {
	return withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		var authorID string
		err := tx.QueryRowContext(ctx, `
			SELECT author_id FROM reed_identities WHERE id = $1 AND server_id = $2
		`, f.ReedID, s.serverID).Scan(&authorID)
		if err == sql.ErrNoRows {
			return refuse(http.StatusNotFound, "Reed not found")
		}
		if err != nil {
			return err
		}
		if isRootIdentity(authorID, s.serverID) {
			return refuse(http.StatusForbidden, "Root's reeds can't be flagged")
		}
		if authorID == f.ReporterID {
			return refuse(http.StatusForbidden, "You can't flag your own reed")
		}
		var flagged bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM reed_flags WHERE reed_id = $1 AND reporter_id = $2)
		`, f.ReedID, f.ReporterID).Scan(&flagged); err != nil {
			return err
		}
		if flagged {
			return ErrAlreadyFlagged
		}
		sigID, err := insertUserSignature(ctx, tx, f.Signature.ID, f.Signature.Armor)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO reed_flags (reed_id, author_id, reporter_id, note, public_key_id, user_signature_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, f.ReedID, authorID, f.ReporterID, f.Note, f.Signature.ID, sigID, f.CreatedAt.UTC())
		return err
	})
}

// writeFlagError answers a refused flag, 409 when it was already flagged.
func (h *Handlers) writeFlagError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrAlreadyFlagged) {
		writeError(w, http.StatusConflict, "already-flagged")
		return
	}
	h.writeAdminError(w, r, err, "Error storing flag")
}

// verifyFlagSignature checks the reporter's signature with their key.
func (h *Handlers) verifyFlagSignature(ctx context.Context, f reedFlag) error {
	key, err := h.resolvePublicKey(ctx, f.Signature.ID)
	if err != nil {
		return err
	}
	if key == nil || key.Revoked {
		return refuse(http.StatusUnauthorized, "Active public key not available")
	}
	payload := buildReedFlagPayload(f.ReedID, f.ReporterID, f.Signature.ID, f.Note, f.CreatedAt)
	if err := h.services.crypto.verifySignature(string(payload), f.Signature.Armor, key.Armor); err != nil {
		return refuse(http.StatusUnauthorized, "signature verification failed")
	}
	return nil
}

// FlagReed handles POST /flags: a member flags a reed. A flag on a reed
// from another server goes to that server, the only one whose admins see it.
func (h *Handlers) FlagReed(w http.ResponseWriter, r *http.Request) {
	callerID, _ := r.Context().Value(userIDKey).(string)
	if callerID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	var msg pb.ReedFlag
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	f := reedFlagFromPB(&msg)
	if f.ReporterID != callerID {
		writeError(w, http.StatusForbidden, "reporterID must be the caller")
		return
	}
	if err := checkFlag(f); err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	if err := h.verifyFlagSignature(r.Context(), f); err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	author, _ := authorOf(identityID(f.ReedID))
	if string(author) == callerID {
		writeError(w, http.StatusForbidden, "You can't flag your own reed")
		return
	}
	if homeServerID, foreign := h.foreignServerOf(string(author)); foreign {
		h.relayFlag(w, r, homeServerID, &msg)
		return
	}
	if err := h.services.db.StoreFlag(r.Context(), f); err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// relayFlag hands a flag to the reed's home server: one call to one peer.
func (h *Handlers) relayFlag(w http.ResponseWriter, r *http.Request, homeServerID string, msg *pb.ReedFlag) {
	peer, err := h.services.db.GetServerByID(r.Context(), homeServerID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading peer")
		return
	}
	if peer == nil {
		writeError(w, http.StatusNotFound, "Reed not found")
		return
	}
	status, err := h.callPeerRelayEndpoint(r.Context(), homeServerID, peer.BaseURL, "/api/federation/relay/flag", msg, nil)
	switch {
	case err == nil && status >= 200 && status < 300:
		w.WriteHeader(http.StatusCreated)
	case status == http.StatusConflict:
		writeError(w, http.StatusConflict, "already-flagged")
	case status == http.StatusForbidden || status == http.StatusNotFound:
		writeError(w, status, "The reed's server refused the flag")
	default:
		h.services.log.GetLogger(r.Context()).Warn().Err(err).Int("status", status).Str("peerServerID", homeServerID).Msg("Peer did not accept flag")
		writeError(w, http.StatusBadGateway, "Couldn't reach the reed's server")
	}
}

// FlagFromPeer receives a flag one of the calling peer's members raised on
// a reed authored here, checked against the reporter's own key.
func (h *Handlers) FlagFromPeer(w http.ResponseWriter, r *http.Request) {
	peerServerID, _ := r.Context().Value(peerServerIDKey).(string)
	if peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var msg pb.ReedFlag
	if err := readRequest(r, &msg); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "flag", false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	f := reedFlagFromPB(&msg)
	err := func() error {
		if _, serverID, ok := parseIdentityID(identityID(f.ReporterID)); !ok || serverID != peerServerID {
			return refuse(http.StatusBadRequest, "The reporter isn't a member of the calling peer")
		}
		if err := checkFlag(f); err != nil {
			return err
		}
		if err := h.verifyFlagSignature(r.Context(), f); err != nil {
			return err
		}
		if err := h.services.db.UpsertRemoteIdentity(r.Context(), f.ReporterID, peerServerID); err != nil {
			return err
		}
		return h.services.db.StoreFlag(r.Context(), f)
	}()
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "flag", err == nil)
	if err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// flaggedReed is a reed with open flags.
type flaggedReed struct {
	ReedID   string
	AuthorID string
	Latest   time.Time
	Flags    []reedFlag
}

// ListOpenFlags pages reeds with open flags that viewerID may see, latest
// flag first. An admin never sees flags on their own reeds; flags whose
// reporter's account was removed are left out.
func (s *DataService) ListOpenFlags(ctx context.Context, viewerID, cursor string, limit int) ([]*flaggedReed, string, error) {
	args := []any{viewerID}
	having := ""
	if cursor != "" {
		c, err := decodeAdminRequestCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, c.CreatedAt, c.ID)
		having = ` HAVING (MAX(f.created_at), f.reed_id) < ($2, $3)`
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.reed_id, f.author_id, MAX(f.created_at)
		FROM reed_flags f
		WHERE f.dismissed_at IS NULL AND f.author_id <> $1
			AND NOT EXISTS (SELECT 1 FROM account_removals ar WHERE ar.user_id = f.reporter_id)
		GROUP BY f.reed_id, f.author_id`+having+fmt.Sprintf(`
		ORDER BY MAX(f.created_at) DESC, f.reed_id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	var reeds []*flaggedReed
	for rows.Next() {
		var fr flaggedReed
		if err := rows.Scan(&fr.ReedID, &fr.AuthorID, &fr.Latest); err != nil {
			rows.Close()
			return nil, "", err
		}
		reeds = append(reeds, &fr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(reeds) > limit {
		reeds = reeds[:limit]
		last := reeds[limit-1]
		next = encodeAdminRequestCursor(adminRequestCursor{CreatedAt: last.Latest, ID: last.ReedID})
	}
	for _, fr := range reeds {
		flagRows, err := s.db.QueryContext(ctx, `
			SELECT f.reporter_id, f.note, f.created_at, us.public_key_id, us.signature
			FROM reed_flags f JOIN user_signatures us ON us.id = f.user_signature_id
			WHERE f.reed_id = $1 AND f.dismissed_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM account_removals ar WHERE ar.user_id = f.reporter_id)
			ORDER BY f.created_at
		`, fr.ReedID)
		if err != nil {
			return nil, "", err
		}
		for flagRows.Next() {
			f := reedFlag{ReedID: fr.ReedID, AuthorID: fr.AuthorID}
			if err := flagRows.Scan(&f.ReporterID, &f.Note, &f.CreatedAt, &f.Signature.ID, &f.Signature.Armor); err != nil {
				flagRows.Close()
				return nil, "", err
			}
			f.CreatedAt = f.CreatedAt.UTC()
			fr.Flags = append(fr.Flags, f)
		}
		flagRows.Close()
		if err := flagRows.Err(); err != nil {
			return nil, "", err
		}
	}
	return reeds, next, nil
}

// DismissFlags closes every open flag on reedID that viewerID may see.
// False when there were none.
func (s *DataService) DismissFlags(ctx context.Context, reedID, viewerID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE reed_flags SET dismissed_at = NOW()
		WHERE reed_id = $1 AND author_id <> $2 AND dismissed_at IS NULL
	`, reedID, viewerID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListFlags handles GET /admin/flags: reeds with open flags.
func (h *Handlers) ListFlags(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	reeds, next, err := h.services.db.ListOpenFlags(r.Context(), callerID, r.URL.Query().Get("cursor"), 50)
	if err != nil {
		h.writeAdminError(w, r, err, "Error listing flags")
		return
	}
	out := &pb.FlaggedReedList{NextCursor: next}
	for _, fr := range reeds {
		item := &pb.FlaggedReed{ReedId: fr.ReedID, AuthorId: fr.AuthorID}
		for _, f := range fr.Flags {
			item.Flags = append(item.Flags, pbReedFlag(f))
		}
		out.Reeds = append(out.Reeds, item)
	}
	writeResponse(w, http.StatusOK, out)
}

// DismissFlags handles POST /admin/flags/{reedID}/dismiss.
func (h *Handlers) DismissFlags(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	dismissed, err := h.services.db.DismissFlags(r.Context(), mux.Vars(r)["reedID"], callerID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error dismissing flags")
		return
	}
	if !dismissed {
		writeError(w, http.StatusNotFound, "No open flags on that reed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
