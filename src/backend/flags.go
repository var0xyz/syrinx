//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// maxFlagNoteChars caps a flag's optional note.
const maxFlagNoteChars = 140

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
		return refuse(http.StatusBadRequest, "bad_reed_id", "Invalid reed ID")
	}
	if countCharacters(f.Note) > maxFlagNoteChars {
		return refuse(http.StatusBadRequest, "bad_note", "A note can't exceed %d characters", maxFlagNoteChars)
	}
	if d := time.Since(f.CreatedAt); d > flagClockSkew || d < -flagClockSkew {
		return refuse(http.StatusBadRequest, "clock_skew", "createdAt is too far from the server's clock")
	}
	owner, serverID, _, ok := parseKeyFingerprint(identityID(f.Signature.ID))
	if !ok || string(canonicalID(serverID, owner)) != f.ReporterID || f.Signature.Armor == "" {
		return refuse(http.StatusBadRequest, "bad_key", "The flag isn't signed by a key of the reporter's")
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
			return refuse(http.StatusNotFound, "reed_not_found", "Reed not found")
		}
		if err != nil {
			return err
		}
		if isRootIdentity(authorID, s.serverID) {
			return refuse(http.StatusForbidden, "root_reed", "Root's reeds can't be flagged")
		}
		if authorID == f.ReporterID {
			return refuse(http.StatusForbidden, "own_reed", "You can't flag your own reed")
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
		return refuse(http.StatusUnauthorized, "bad_key", "Active public key not available")
	}
	payload := buildReedFlagPayload(f.ReedID, f.ReporterID, f.Signature.ID, f.Note, f.CreatedAt)
	if err := h.services.crypto.verifySignature(string(payload), f.Signature.Armor, key.Armor); err != nil {
		return refuse(http.StatusUnauthorized, "bad_signature", "signature verification failed")
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
	h.metrics.FlagCreated(r.Context(), f.ReporterID, string(author), f.ReedID, metrics.OriginLocal, f.Note != "")
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
			return refuse(http.StatusBadRequest, "foreign_reporter", "The reporter isn't a member of the calling peer")
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
	author, _ := authorOf(identityID(f.ReedID))
	h.metrics.FlagCreated(r.Context(), f.ReporterID, string(author), f.ReedID, metrics.OriginPeer, f.Note != "")
	w.WriteHeader(http.StatusCreated)
}

// maxDismissalReasonChars caps the reason an admin gives for a dismissal.
const maxDismissalReasonChars = 140

// flagDismissal is an admin closing a reed's open flags. DismissedBy is
// empty once their account is gone.
type flagDismissal struct {
	ID          int64
	DismissedBy string
	Reason      string
	DismissedAt time.Time
	// Flags is how many flags it closed, set only when it's made.
	Flags int
}

func pbFlagDismissal(d *flagDismissal) *pb.FlagDismissal {
	return &pb.FlagDismissal{
		Id:          strconv.FormatInt(d.ID, 10),
		DismissedBy: d.DismissedBy,
		Reason:      d.Reason,
		DismissedAt: unixOrZero(d.DismissedAt),
	}
}

// flaggedReed is a reed with open flags, or the flags one dismissal closed.
type flaggedReed struct {
	ReedID    string
	AuthorID  string
	Latest    time.Time
	Flags     []reedFlag
	Dismissal *flagDismissal
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
		WHERE f.dismissal_id IS NULL AND f.author_id <> $1
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
	return reeds, next, s.loadReedFlags(ctx, reeds)
}

// ListDismissedFlags pages dismissals viewerID may see, latest first, each
// with the flags it closed.
func (s *DataService) ListDismissedFlags(ctx context.Context, viewerID, cursor string, limit int) ([]*flaggedReed, string, error) {
	args := []any{viewerID}
	after := ""
	if cursor != "" {
		c, err := decodeAdminRequestCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, c.CreatedAt, c.ID)
		after = ` AND (d.dismissed_at, d.id) < ($2, $3::bigint)`
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.reed_id, d.author_id, COALESCE(d.dismissed_by, ''), d.reason, d.dismissed_at
		FROM flag_dismissals d
		WHERE d.author_id <> $1`+after+fmt.Sprintf(`
		ORDER BY d.dismissed_at DESC, d.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	var reeds []*flaggedReed
	for rows.Next() {
		fr := flaggedReed{Dismissal: &flagDismissal{}}
		d := fr.Dismissal
		if err := rows.Scan(&d.ID, &fr.ReedID, &fr.AuthorID, &d.DismissedBy, &d.Reason, &d.DismissedAt); err != nil {
			rows.Close()
			return nil, "", err
		}
		d.DismissedAt = d.DismissedAt.UTC()
		reeds = append(reeds, &fr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(reeds) > limit {
		reeds = reeds[:limit]
		last := reeds[limit-1].Dismissal
		next = encodeAdminRequestCursor(adminRequestCursor{CreatedAt: last.DismissedAt, ID: strconv.FormatInt(last.ID, 10)})
	}
	return reeds, next, s.loadReedFlags(ctx, reeds)
}

// loadReedFlags fills each reed's flags: its open ones, or the ones its
// dismissal closed. Flags whose reporter's account was removed are left out.
func (s *DataService) loadReedFlags(ctx context.Context, reeds []*flaggedReed) error {
	for _, fr := range reeds {
		var dismissalID sql.NullInt64
		if fr.Dismissal != nil {
			dismissalID = sql.NullInt64{Int64: fr.Dismissal.ID, Valid: true}
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT f.reporter_id, f.note, f.created_at, us.public_key_id, us.signature
			FROM reed_flags f JOIN user_signatures us ON us.id = f.user_signature_id
			WHERE f.reed_id = $1 AND f.dismissal_id IS NOT DISTINCT FROM $2::bigint
				AND NOT EXISTS (SELECT 1 FROM account_removals ar WHERE ar.user_id = f.reporter_id)
			ORDER BY f.created_at
		`, fr.ReedID, dismissalID)
		if err != nil {
			return err
		}
		for rows.Next() {
			f := reedFlag{ReedID: fr.ReedID, AuthorID: fr.AuthorID}
			if err := rows.Scan(&f.ReporterID, &f.Note, &f.CreatedAt, &f.Signature.ID, &f.Signature.Armor); err != nil {
				rows.Close()
				return err
			}
			f.CreatedAt = f.CreatedAt.UTC()
			fr.Flags = append(fr.Flags, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// DismissFlags closes every open flag on reedID that viewerID may see,
// recording who did it and why.
func (s *DataService) DismissFlags(ctx context.Context, reedID, viewerID, reason string) (*flagDismissal, error) {
	d := &flagDismissal{DismissedBy: viewerID, Reason: reason}
	err := withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		var authorID string
		err := tx.QueryRowContext(ctx, `
			SELECT author_id FROM reed_flags
			WHERE reed_id = $1 AND author_id <> $2 AND dismissal_id IS NULL
			LIMIT 1 FOR UPDATE
		`, reedID, viewerID).Scan(&authorID)
		if err == sql.ErrNoRows {
			return refuse(http.StatusNotFound, "no_open_flags", "No open flags on that reed")
		}
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO flag_dismissals (reed_id, author_id, dismissed_by, reason, dismissed_at)
			VALUES ($1, $2, $3, $4, NOW() AT TIME ZONE 'UTC')
			RETURNING id, dismissed_at
		`, reedID, authorID, viewerID, reason).Scan(&d.ID, &d.DismissedAt); err != nil {
			return err
		}
		d.DismissedAt = d.DismissedAt.UTC()
		res, err := tx.ExecContext(ctx, `
			UPDATE reed_flags SET dismissal_id = $2
			WHERE reed_id = $1 AND dismissal_id IS NULL
		`, reedID, d.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		d.Flags = int(n)
		return err
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListFlags handles GET /admin/flags: reeds with open flags, or with
// state=dismissed, past dismissals.
func (h *Handlers) ListFlags(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	query := r.URL.Query()
	list := h.services.db.ListOpenFlags
	switch query.Get("state") {
	case "", "open":
	case "dismissed":
		list = h.services.db.ListDismissedFlags
	default:
		writeError(w, http.StatusBadRequest, "state must be open or dismissed")
		return
	}
	reeds, next, err := list(r.Context(), callerID, query.Get("cursor"), 50)
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
		if fr.Dismissal != nil {
			item.Dismissal = pbFlagDismissal(fr.Dismissal)
		}
		out.Reeds = append(out.Reeds, item)
	}
	writeResponse(w, http.StatusOK, out)
}

// DismissFlags handles POST /admin/flags/{reedID}/dismiss. A reason is
// required; the dismissal stays off the ledger.
func (h *Handlers) DismissFlags(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	var msg pb.DismissFlagsRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reason := strings.TrimSpace(msg.GetReason())
	if reason == "" || countCharacters(reason) > maxDismissalReasonChars {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("A reason of 1 to %d characters is required", maxDismissalReasonChars))
		return
	}
	reedID := mux.Vars(r)["reedID"]
	d, err := h.services.db.DismissFlags(r.Context(), reedID, callerID, reason)
	if err != nil {
		h.writeAdminError(w, r, err, "Error dismissing flags")
		return
	}
	h.metrics.FlagsDismissed(r.Context(), callerID, reedID, d.Flags)
	writeResponse(w, http.StatusOK, pbFlagDismissal(d))
}
