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
	"time"

	"github.com/gorilla/mux"
	"github.com/lib/pq"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// maxFlagNoteChars caps a flag's optional note.
const maxFlagNoteChars = 140

// ErrAlreadyFlagged: the reporter already flagged that reed.
var ErrAlreadyFlagged = errors.New("already flagged")

// reedFlag is a flag as stored and as it travels. ServerSignature is the
// reed's home server's, empty until it takes the flag.
type reedFlag struct {
	ReedID          string
	AuthorID        string
	ReporterID      string
	Note            string
	Signature       UserSignature
	ServerSignature ServerSignature
}

func reedFlagFromPB(msg *pb.ReedFlag) reedFlag {
	return reedFlag{
		ReedID:          strings.TrimSpace(msg.GetReedId()),
		ReporterID:      msg.GetReporterId(),
		Note:            msg.GetNote(),
		Signature:       UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
		ServerSignature: serverSignatureFromPB(msg.GetServerSignature()),
	}
}

func pbReedFlag(f reedFlag) *pb.ReedFlag {
	return &pb.ReedFlag{
		ReedId:          f.ReedID,
		ReporterId:      f.ReporterID,
		Note:            f.Note,
		Signature:       pbUserSignature(f.Signature),
		ServerSignature: pbServerSignature(f.ServerSignature),
	}
}

// checkFlag refuses a flag that isn't well formed: a reed ID, a short note
// and a key of the reporter's.
func checkFlag(f reedFlag) error {
	if _, ok := authorOf(identityID(f.ReedID)); !ok {
		return refuse(http.StatusBadRequest, "bad_reed_id", "Invalid reed ID")
	}
	if countCharacters(f.Note) > maxFlagNoteChars {
		return refuse(http.StatusBadRequest, "bad_note", "A note can't exceed %d characters", maxFlagNoteChars)
	}
	owner, serverID, _, ok := parseKeyFingerprint(identityID(f.Signature.ID))
	if !ok || string(canonicalID(serverID, owner)) != f.ReporterID || f.Signature.Armor == "" {
		return refuse(http.StatusBadRequest, "bad_key", "The flag isn't signed by a key of the reporter's")
	}
	return nil
}

// StoreFlag stores a verified, countersigned flag on a reed authored here.
// The reed's author is read from its identity row.
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
		return insertReedFlagTx(ctx, tx, f, authorID)
	})
}

// insertReedFlagTx writes f's row and both its signatures.
func insertReedFlagTx(ctx context.Context, tx *sql.Tx, f reedFlag, authorID string) error {
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, f.Signature, f.ServerSignature)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO reed_flags (reed_id, author_id, reporter_id, note, public_key_id,
			user_signature_id, server_signature_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, f.ReedID, authorID, f.ReporterID, f.Note, f.Signature.ID, userSigID, serverSigID, f.ServerSignature.SignedAt.UTC())
	return err
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
	payload := buildReedFlagPayload(f.ReedID, f.ReporterID, f.Signature.ID, f.Note)
	if err := h.services.crypto.verifySignature(ctx, string(payload), f.Signature.Armor, key.Armor); err != nil {
		return refuse(http.StatusUnauthorized, "bad_signature", "signature verification failed")
	}
	return nil
}

// storeFlag countersigns a verified flag on a reed authored here, now, and
// stores it.
func (h *Handlers) storeFlag(ctx context.Context, f *reedFlag) error {
	now := time.Now().UTC().Truncate(time.Second)
	payload := buildReedFlagServerPayload(f.ReedID, f.ReporterID, h.signingKey.Fingerprint, f.Signature.Armor, now)
	sig, err := h.countersign(ctx, payload, now)
	if err != nil {
		return err
	}
	f.ServerSignature = sig
	if err := h.services.db.StoreFlag(ctx, *f); err != nil {
		return err
	}
	h.pushAdminRecord(ctx, adminRecord{Flag: f})
	return nil
}

// FlagReed handles POST /flags: a member flags a reed, and gets it back
// countersigned. A flag on another server's reed goes to that server.
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
	if err := h.storeFlag(r.Context(), &f); err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	h.metrics.FlagCreated(r.Context(), f.ReporterID, string(author), f.ReedID, metrics.OriginLocal, f.Note != "")
	writeResponse(w, http.StatusCreated, pbReedFlag(f))
}

// relayFlag hands a flag to the reed's home server, one call to one peer,
// and answers with the flag as that server countersigned it.
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
	var countersigned pb.ReedFlag
	status, err := h.callPeerRelayEndpoint(r.Context(), homeServerID, peer.BaseURL, "/api/federation/relay/flag", msg, &countersigned)
	switch {
	case err == nil && status >= 200 && status < 300:
		writeResponse(w, http.StatusCreated, &countersigned)
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
		return h.storeFlag(r.Context(), &f)
	}()
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "flag", err == nil)
	if err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	author, _ := authorOf(identityID(f.ReedID))
	h.metrics.FlagCreated(r.Context(), f.ReporterID, string(author), f.ReedID, metrics.OriginPeer, f.Note != "")
	writeResponse(w, http.StatusCreated, pbReedFlag(f))
}

// maxDismissalReasonChars caps the reason an admin gives for a dismissal.
const maxDismissalReasonChars = 140

// ErrFlagsChanged: a dismissal names a flag that isn't open anymore.
var ErrFlagsChanged = errors.New("flags changed")

// flagDismissal is an admin closing the open flags Reporters, sorted,
// raised on a reed. DismissedBy is empty once their account is gone.
type flagDismissal struct {
	ID              string
	ReedID          string
	DismissedBy     string
	Reason          string
	Reporters       []string
	Signature       UserSignature
	ServerSignature ServerSignature
}

func (d *flagDismissal) payload() []byte {
	return buildFlagDismissalPayload(d.ID, d.ReedID, d.Reason, d.Signature.ID, d.Reporters)
}

func pbFlagDismissal(d *flagDismissal) *pb.FlagDismissal {
	out := &pb.FlagDismissal{
		Id:              d.ID,
		ReedId:          d.ReedID,
		DismissedBy:     d.DismissedBy,
		Reason:          d.Reason,
		Reporters:       d.Reporters,
		Signature:       pbUserSignature(d.Signature),
		ServerSignature: pbServerSignature(d.ServerSignature),
	}
	return out
}

func flagDismissalFromPB(msg *pb.FlagDismissal) *flagDismissal {
	d := &flagDismissal{
		ID:              msg.GetId(),
		ReedID:          msg.GetReedId(),
		DismissedBy:     msg.GetDismissedBy(),
		Reason:          msg.GetReason(),
		Reporters:       msg.GetReporters(),
		Signature:       UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
		ServerSignature: serverSignatureFromPB(msg.GetServerSignature()),
	}
	return d
}

// flaggedReed is a reed with its open flags, or with one dismissal and the
// flags it closed.
type flaggedReed struct {
	ReedID    string
	AuthorID  string
	Latest    time.Time
	Flags     []reedFlag
	Dismissal *flagDismissal
}

// ListOpenFlags pages reeds with open flags that viewerID may see, latest
// flag first. An admin never sees flags on their own reeds.
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
		after = ` AND (d.dismissed_at, d.id) < ($2, $3)`
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, flagDismissalSelect+`
		WHERE d.author_id <> $1`+after+fmt.Sprintf(`
		ORDER BY d.dismissed_at DESC, d.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	var reeds []*flaggedReed
	for rows.Next() {
		var fr flaggedReed
		if fr.Dismissal, err = scanFlagDismissal(rows, &fr.AuthorID); err != nil {
			rows.Close()
			return nil, "", err
		}
		fr.ReedID = fr.Dismissal.ReedID
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
		next = encodeAdminRequestCursor(adminRequestCursor{CreatedAt: last.ServerSignature.SignedAt, ID: last.ID})
	}
	return reeds, next, s.loadReedFlags(ctx, reeds)
}

const flagDismissalSelect = `
	SELECT d.id, d.reed_id, d.author_id, COALESCE(d.dismissed_by, ''), d.reason,
		us.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
	FROM flag_dismissals d
	JOIN user_signatures us ON us.id = d.user_signature_id
	JOIN server_signatures ss ON ss.id = d.server_signature_id`

func scanFlagDismissal(row interface{ Scan(...any) error }, authorID *string) (*flagDismissal, error) {
	d := &flagDismissal{}
	if err := row.Scan(&d.ID, &d.ReedID, authorID, &d.DismissedBy, &d.Reason,
		&d.Signature.ID, &d.Signature.Armor,
		&d.ServerSignature.ID, &d.ServerSignature.Armor, &d.ServerSignature.SignedAt); err != nil {
		return nil, err
	}
	d.ServerSignature.SignedAt = d.ServerSignature.SignedAt.UTC()
	return d, nil
}

// loadReedFlags fills each reed's open flags, or its dismissal's.
func (s *DataService) loadReedFlags(ctx context.Context, reeds []*flaggedReed) error {
	for _, fr := range reeds {
		dismissalID := sql.NullString{}
		if fr.Dismissal != nil {
			dismissalID = sql.NullString{String: fr.Dismissal.ID, Valid: true}
		}
		flags, err := queryReedFlags(ctx, s.db, fr.ReedID, fr.AuthorID, dismissalID)
		if err != nil {
			return err
		}
		fr.Flags = flags
		if fr.Dismissal != nil {
			if fr.Dismissal.Reporters, err = queryDismissalReporters(ctx, s.db, fr.Dismissal.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func queryDismissalReporters(ctx context.Context, q adminQuerier, dismissalID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT reporter_id FROM flag_dismissal_reporters WHERE dismissal_id = $1 ORDER BY reporter_id COLLATE "C"
	`, dismissalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// queryReedFlags lists reedID's open flags, or the ones dismissalID closed.
func queryReedFlags(ctx context.Context, q adminQuerier, reedID, authorID string, dismissalID sql.NullString) ([]reedFlag, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT f.reporter_id, f.note, us.public_key_id, us.signature,
			ss.private_key_id, ss.signature, ss.signed_at
		FROM reed_flags f
		JOIN user_signatures us ON us.id = f.user_signature_id
		JOIN server_signatures ss ON ss.id = f.server_signature_id
		WHERE f.reed_id = $1 AND f.dismissal_id IS NOT DISTINCT FROM $2
		ORDER BY f.created_at
	`, reedID, dismissalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var flags []reedFlag
	for rows.Next() {
		f := reedFlag{ReedID: reedID, AuthorID: authorID}
		if err := rows.Scan(&f.ReporterID, &f.Note, &f.Signature.ID, &f.Signature.Armor,
			&f.ServerSignature.ID, &f.ServerSignature.Armor, &f.ServerSignature.SignedAt); err != nil {
			return nil, err
		}
		f.ServerSignature.SignedAt = f.ServerSignature.SignedAt.UTC()
		flags = append(flags, f)
	}
	return flags, rows.Err()
}

// DismissFlags closes the open flags d's reporters raised on its reed,
// which viewerID may see. A reporter whose flag isn't open refuses it all
// with ErrFlagsChanged.
func (s *DataService) DismissFlags(ctx context.Context, d *flagDismissal, viewerID string) error {
	return withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		var authorID string
		err := tx.QueryRowContext(ctx, `
			SELECT author_id FROM reed_flags
			WHERE reed_id = $1 AND author_id <> $2 AND dismissal_id IS NULL
			LIMIT 1 FOR UPDATE
		`, d.ReedID, viewerID).Scan(&authorID)
		if err == sql.ErrNoRows {
			return refuse(http.StatusNotFound, "no_open_flags", "No open flags on that reed")
		}
		if err != nil {
			return err
		}
		var open int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM reed_flags
			WHERE reed_id = $1 AND reporter_id = ANY($2) AND dismissal_id IS NULL
		`, d.ReedID, pq.Array(d.Reporters)).Scan(&open); err != nil {
			return err
		}
		if open != len(d.Reporters) {
			return ErrFlagsChanged
		}
		return insertFlagDismissalTx(ctx, tx, d, authorID)
	})
}

// insertFlagDismissalTx writes d and closes with it the open flags of its
// reporters held here.
func insertFlagDismissalTx(ctx context.Context, tx *sql.Tx, d *flagDismissal, authorID string) error {
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, d.Signature, d.ServerSignature)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO flag_dismissals (id, reed_id, author_id, dismissed_by, reason,
			public_key_id, user_signature_id, server_signature_id, dismissed_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9)
	`, d.ID, d.ReedID, authorID, d.DismissedBy, d.Reason, d.Signature.ID, userSigID, serverSigID,
		d.ServerSignature.SignedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO flag_dismissal_reporters (dismissal_id, reporter_id) SELECT $1, unnest($2::text[])
	`, d.ID, pq.Array(d.Reporters)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE reed_flags SET dismissal_id = $2
		WHERE reed_id = $1 AND reporter_id = ANY($3) AND dismissal_id IS NULL
	`, d.ReedID, d.ID, pq.Array(d.Reporters))
	return err
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

// DismissFlags handles POST /admin/flags/{reedID}/dismiss: an admin's
// signed dismissal of the open flags it names. A reason is required; the
// dismissal stays off the ledger.
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
	d := &flagDismissal{
		ID:          msg.GetId(),
		ReedID:      mux.Vars(r)["reedID"],
		DismissedBy: callerID,
		Reason:      strings.TrimSpace(msg.GetReason()),
		Reporters:   msg.GetReporters(),
		Signature:   UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
	}
	if err := h.checkFlagDismissal(r.Context(), d, msg.GetReason()); err != nil {
		h.writeAdminError(w, r, err, "Error dismissing flags")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	sig, err := h.countersign(r.Context(), buildFlagDismissalServerPayload(d.ID, d.ReedID, h.signingKey.Fingerprint, d.Signature.Armor, now), now)
	if err != nil {
		h.writeAdminError(w, r, err, "Error countersigning dismissal")
		return
	}
	d.ServerSignature = sig
	if err := h.services.db.DismissFlags(r.Context(), d, callerID); err != nil {
		if errors.Is(err, ErrFlagsChanged) {
			writeError(w, http.StatusConflict, "flags-changed")
			return
		}
		h.writeAdminError(w, r, err, "Error dismissing flags")
		return
	}
	h.metrics.FlagsDismissed(r.Context(), callerID, d.ReedID, len(d.Reporters))
	h.pushAdminRecord(r.Context(), adminRecord{Dismissal: d})
	writeResponse(w, http.StatusOK, pbFlagDismissal(d))
}

// checkFlagDismissal refuses a dismissal that isn't well formed or isn't
// signed by an active key of its admin's over the reporters it names.
func (h *Handlers) checkFlagDismissal(ctx context.Context, d *flagDismissal, rawReason string) error {
	if !isValidUUIDv7(d.ID) {
		return refuse(http.StatusBadRequest, "bad_id", "A dismissal ID must be a UUIDv7")
	}
	if d.Reason == "" || d.Reason != rawReason || countCharacters(d.Reason) > maxDismissalReasonChars {
		return refuse(http.StatusBadRequest, "bad_reason", "A reason of 1 to %d characters is required", maxDismissalReasonChars)
	}
	if !validReporters(d.Reporters) {
		return refuse(http.StatusBadRequest, "bad_reporters", "reporters must be sorted, distinct and not empty")
	}
	if owner, ok := authorOf(identityID(d.Signature.ID)); !ok || string(owner) != d.DismissedBy || d.Signature.Armor == "" {
		return refuse(http.StatusBadRequest, "bad_key", "The dismissal isn't signed by a key of yours")
	}
	key, err := h.resolvePublicKey(ctx, d.Signature.ID)
	if err != nil {
		return err
	}
	if key == nil || key.Revoked {
		return refuse(http.StatusUnauthorized, "bad_key", "Active public key not available")
	}
	if err := h.services.crypto.verifySignature(ctx, string(d.payload()), d.Signature.Armor, key.Armor); err != nil {
		return refuse(http.StatusUnauthorized, "bad_signature", "signature verification failed")
	}
	return nil
}

// validReporters reports whether reporters is sorted, distinct and not empty.
func validReporters(reporters []string) bool {
	return len(reporters) > 0 && slices.IsSorted(reporters) && len(slices.Compact(slices.Clone(reporters))) == len(reporters)
}
