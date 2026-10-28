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

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/lib/pq"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// maxFlagNoteChars caps a flag's optional note.
const maxFlagNoteChars = 140

// ErrAlreadyFlagged: the reporter already flagged that reed.
var ErrAlreadyFlagged = errors.New("already flagged")

// ErrFlagAcknowledged: the flag's case was dismissed, so it can't be
// withdrawn.
var ErrFlagAcknowledged = errors.New("flag acknowledged")

// errFlagFrozen refuses acting on a case recovery brought back while the
// server is still in lockdown.
var errFlagFrozen = refuse(http.StatusConflict, "opened_before_recovery", "opened-before-recovery")

// reedFlag is a flag as stored and as it travels. ServerSignature is the
// reed's home server's, empty until it takes the flag and puts it in
// CaseID. Dismissal is set on admins' copies once the case is dismissed.
type reedFlag struct {
	ReedID          string
	AuthorID        string
	ReporterID      string
	Note            string
	CaseID          string
	Signature       UserSignature
	ServerSignature ServerSignature
	Withdrawal      *flagWithdrawal
	Dismissal       *flagDismissal
}

// flagWithdrawal is the reporter taking back their flag, signed over its
// signature. ServerSignature is empty until the reed's server takes it.
type flagWithdrawal struct {
	Signature       UserSignature
	ServerSignature ServerSignature
}

func (f *reedFlag) withdrawalPayload() []byte {
	return buildFlagWithdrawalPayload(f.ReedID, f.ReporterID, f.Withdrawal.Signature.ID, f.Signature.Armor)
}

func reedFlagFromPB(msg *pb.ReedFlag) reedFlag {
	f := reedFlag{
		ReedID:          strings.TrimSpace(msg.GetReedId()),
		ReporterID:      msg.GetReporterId(),
		Note:            msg.GetNote(),
		CaseID:          msg.GetCaseId(),
		Signature:       UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
		ServerSignature: serverSignatureFromPB(msg.GetServerSignature()),
	}
	if d := msg.GetDismissal(); d != nil {
		f.Dismissal = flagDismissalFromPB(d)
	}
	if w := msg.GetWithdrawal(); w != nil {
		f.Withdrawal = &flagWithdrawal{
			Signature:       UserSignature{ID: w.GetSignature().GetId(), Armor: w.GetSignature().GetArmor()},
			ServerSignature: serverSignatureFromPB(w.GetServerSignature()),
		}
	}
	return f
}

func pbReedFlag(f reedFlag) *pb.ReedFlag {
	out := &pb.ReedFlag{
		ReedId:          f.ReedID,
		ReporterId:      f.ReporterID,
		Note:            f.Note,
		CaseId:          f.CaseID,
		Signature:       pbUserSignature(f.Signature),
		ServerSignature: pbServerSignature(f.ServerSignature),
	}
	if f.Dismissal != nil {
		out.Dismissal = pbFlagDismissal(f.Dismissal)
	}
	if f.Withdrawal != nil {
		out.Withdrawal = &pb.FlagWithdrawal{
			Signature:       pbUserSignature(f.Withdrawal.Signature),
			ServerSignature: pbServerSignature(f.Withdrawal.ServerSignature),
		}
	}
	return out
}

// openCase holds for a flag_cases row c neither dismissed nor void.
const openCase = `c.dismissed_at IS NULL AND c.voided_at IS NULL`

// openFlag holds for a flag of reed_flags f its reporter hasn't withdrawn,
// in a case that is still open.
const openFlag = `f.withdrawal_signature_id IS NULL AND EXISTS (
	SELECT 1 FROM flag_cases c WHERE c.id = f.case_id AND ` + openCase + `)`

const reedFlagSelect = `
	SELECT f.reed_id, f.author_id, f.reporter_id, f.note, f.case_id, us.public_key_id, us.signature,
		ss.private_key_id, ss.signature, ss.signed_at,
		wus.public_key_id, wus.signature, wss.private_key_id, wss.signature, wss.signed_at
	FROM reed_flags f
	JOIN user_signatures us ON us.id = f.user_signature_id
	JOIN server_signatures ss ON ss.id = f.server_signature_id
	LEFT JOIN user_signatures wus ON wus.id = f.withdrawal_signature_id
	LEFT JOIN server_signatures wss ON wss.id = f.withdrawal_server_signature_id`

func scanReedFlag(row interface{ Scan(...any) error }) (reedFlag, error) {
	var f reedFlag
	var wKey, wArmor, wServerKey, wServerArmor sql.NullString
	var wSignedAt sql.NullTime
	if err := row.Scan(&f.ReedID, &f.AuthorID, &f.ReporterID, &f.Note, &f.CaseID, &f.Signature.ID, &f.Signature.Armor,
		&f.ServerSignature.ID, &f.ServerSignature.Armor, &f.ServerSignature.SignedAt,
		&wKey, &wArmor, &wServerKey, &wServerArmor, &wSignedAt); err != nil {
		return f, err
	}
	f.ServerSignature.SignedAt = f.ServerSignature.SignedAt.UTC()
	if wKey.Valid {
		f.Withdrawal = &flagWithdrawal{
			Signature:       UserSignature{ID: wKey.String, Armor: wArmor.String},
			ServerSignature: ServerSignature{ID: wServerKey.String, Armor: wServerArmor.String, SignedAt: wSignedAt.Time.UTC()},
		}
	}
	return f, nil
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

// StoreFlag puts a verified flag on a reed authored here in the reed's open
// case, opening one if there is none, has sign countersign it naming that
// case, and stores it. The reed's author is read from its identity row.
func (s *DataService) StoreFlag(ctx context.Context, f *reedFlag, sign func(caseID string, at time.Time) (ServerSignature, error)) error {
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
		now := time.Now().UTC().Truncate(time.Second)
		caseID, err := openCaseTx(ctx, tx, f.ReedID, now)
		if err != nil {
			return err
		}
		if f.ServerSignature, err = sign(caseID, now); err != nil {
			return err
		}
		f.CaseID, f.AuthorID = caseID, authorID
		return insertReedFlagTx(ctx, tx, *f, authorID)
	})
}

// openCaseTx returns reedID's open case, opening one at at when there is
// none. A concurrent opener loses on idx_flag_cases_open and joins it.
func openCaseTx(ctx context.Context, tx *sql.Tx, reedID string, at time.Time) (string, error) {
	for range 2 {
		var id string
		err := tx.QueryRowContext(ctx, `
			SELECT c.id FROM flag_cases c WHERE c.reed_id = $1 AND `+openCase+` AND NOT c.restored FOR UPDATE
		`, reedID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		err = tx.QueryRowContext(ctx, `
			INSERT INTO flag_cases (id, reed_id, opened_at) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING RETURNING id
		`, uuid.Must(uuid.NewV7()).String(), reedID, at).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	return "", fmt.Errorf("no open case for %s", reedID)
}

// insertReedFlagTx writes f's row, in f.CaseID, and both its signatures.
func insertReedFlagTx(ctx context.Context, tx *sql.Tx, f reedFlag, authorID string) error {
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, f.Signature, f.ServerSignature)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO reed_flags (reed_id, author_id, reporter_id, note, public_key_id,
			user_signature_id, server_signature_id, created_at, case_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, f.ReedID, authorID, f.ReporterID, f.Note, f.Signature.ID, userSigID, serverSigID, f.ServerSignature.SignedAt.UTC(), f.CaseID)
	return err
}

// writeFlagError answers a refused flag, 409 when it was already flagged.
func (h *Handlers) writeFlagError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrAlreadyFlagged) {
		writeError(w, http.StatusConflict, "already-flagged")
		return
	}
	if errors.Is(err, ErrFlagAcknowledged) {
		writeError(w, http.StatusConflict, "flag-acknowledged")
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

// storeFlag stores a verified flag on a reed authored here in its open
// case, countersigned now.
func (h *Handlers) storeFlag(ctx context.Context, f *reedFlag) error {
	sign := func(caseID string, at time.Time) (ServerSignature, error) {
		return h.countersign(ctx, buildReedFlagServerPayload(f.ReedID, f.ReporterID, caseID, h.signingKey.Fingerprint, f.Signature.Armor, at), at)
	}
	if err := h.services.db.StoreFlag(ctx, f, sign); err != nil {
		return err
	}
	author, _ := authorOf(identityID(f.ReedID))
	h.reportOpenFlags(ctx, string(author))
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

// flagCaseRetention is how long a closed case is kept before an admin may
// delete it.
const flagCaseRetention = 183 * 24 * time.Hour

// flagDismissal is an admin closing a case. DismissedBy is empty once their
// account is gone.
type flagDismissal struct {
	CaseID          string
	ReedID          string
	DismissedBy     string
	Reason          string
	Signature       UserSignature
	ServerSignature ServerSignature
}

func (d *flagDismissal) payload() []byte {
	return buildFlagDismissalPayload(d.CaseID, d.ReedID, d.Reason, d.Signature.ID)
}

func pbFlagDismissal(d *flagDismissal) *pb.FlagDismissal {
	return &pb.FlagDismissal{
		CaseId:          d.CaseID,
		ReedId:          d.ReedID,
		DismissedBy:     d.DismissedBy,
		Reason:          d.Reason,
		Signature:       pbUserSignature(d.Signature),
		ServerSignature: pbServerSignature(d.ServerSignature),
	}
}

func flagDismissalFromPB(msg *pb.FlagDismissal) *flagDismissal {
	return &flagDismissal{
		CaseID:          msg.GetCaseId(),
		ReedID:          msg.GetReedId(),
		DismissedBy:     msg.GetDismissedBy(),
		Reason:          msg.GetReason(),
		Signature:       UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
		ServerSignature: serverSignatureFromPB(msg.GetServerSignature()),
	}
}

// flagCase is a reed's flags from the first open one until a dismissal
// closes them or all are withdrawn. Restored ones came back in recovery.
type flagCase struct {
	ID        string
	ReedID    string
	AuthorID  string
	OpenedAt  time.Time
	Restored  bool
	Latest    time.Time
	Flags     []reedFlag
	Dismissal *flagDismissal
	VoidedAt  *time.Time
}

func pbFlagCase(c *flagCase) *pb.FlagCase {
	out := &pb.FlagCase{
		Id:       c.ID,
		ReedId:   c.ReedID,
		AuthorId: c.AuthorID,
		OpenedAt: unixOrZero(c.OpenedAt),
		VoidedAt: unixOrZeroAt(c.VoidedAt),
	}
	for _, f := range c.Flags {
		out.Flags = append(out.Flags, pbReedFlag(f))
	}
	if c.Dismissal != nil {
		out.Dismissal = pbFlagDismissal(c.Dismissal)
	}
	return out
}

// flagCaseColumns selects a case of flag_cases c, in scanFlagCase's order;
// flagCaseFrom is where they come from.
const flagCaseColumns = `
	SELECT c.id, c.reed_id, ri.author_id, c.opened_at, c.restored, c.voided_at,
		COALESCE(c.dismissed_by, ''), COALESCE(c.reason, ''), COALESCE(us.public_key_id, ''), COALESCE(us.signature, ''),
		COALESCE(ss.private_key_id, ''), COALESCE(ss.signature, ''), ss.signed_at,
		COALESCE((SELECT MAX(f.created_at) FROM reed_flags f WHERE f.case_id = c.id), c.opened_at)`

const flagCaseFrom = `
	FROM flag_cases c
	JOIN reed_identities ri ON ri.id = c.reed_id
	LEFT JOIN user_signatures us ON us.id = c.user_signature_id
	LEFT JOIN server_signatures ss ON ss.id = c.server_signature_id`

// scanFlagCase scans flagCaseSelect's columns, then extra.
func scanFlagCase(row interface{ Scan(...any) error }, extra ...any) (*flagCase, error) {
	c := &flagCase{}
	var voidedAt, dismissedAt sql.NullTime
	d := flagDismissal{}
	dest := append([]any{&c.ID, &c.ReedID, &c.AuthorID, &c.OpenedAt, &c.Restored, &voidedAt,
		&d.DismissedBy, &d.Reason, &d.Signature.ID, &d.Signature.Armor,
		&d.ServerSignature.ID, &d.ServerSignature.Armor, &dismissedAt, &c.Latest}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	c.OpenedAt, c.Latest = c.OpenedAt.UTC(), c.Latest.UTC()
	c.VoidedAt = secondOf(voidedAt)
	if dismissedAt.Valid {
		d.CaseID, d.ReedID = c.ID, c.ReedID
		d.ServerSignature.SignedAt = dismissedAt.Time.UTC()
		c.Dismissal = &d
	}
	return c, nil
}

// Case states a listing can ask for.
const (
	flagCasesOpen      = "open"
	flagCasesDismissed = "dismissed"
	flagCasesVoid      = "void"
)

// ListFlagCases pages the cases in state viewerID may see, newest first:
// open ones by their latest flag, dismissed ones by dismissal, void ones
// by when they went void. An admin never sees cases on their own reeds.
func (s *DataService) ListFlagCases(ctx context.Context, viewerID, state, cursor string, limit int) ([]*flagCase, string, error) {
	var where, ord string
	switch state {
	case flagCasesOpen:
		where, ord = openCase, `COALESCE((SELECT MAX(f.created_at) FROM reed_flags f WHERE f.case_id = c.id), c.opened_at)`
	case flagCasesDismissed:
		where, ord = `c.dismissed_at IS NOT NULL`, `c.dismissed_at`
	case flagCasesVoid:
		where, ord = `c.voided_at IS NOT NULL`, `c.voided_at`
	default:
		return nil, "", refuse(http.StatusBadRequest, "bad_state", "state must be open, dismissed or void")
	}
	args := []any{viewerID}
	after := ""
	if cursor != "" {
		cur, err := decodeAdminRequestCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, cur.CreatedAt, cur.ID)
		after = ` AND (` + ord + `, c.id) < ($2, $3)`
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, flagCaseColumns+`, `+ord+flagCaseFrom+`
		WHERE `+where+` AND ri.author_id <> $1`+after+fmt.Sprintf(`
		ORDER BY `+ord+` DESC, c.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	var cases []*flagCase
	var ords []time.Time
	for rows.Next() {
		var at time.Time
		c, err := scanFlagCase(rows, &at)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		cases = append(cases, c)
		ords = append(ords, at)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(cases) > limit {
		cases = cases[:limit]
		next = encodeAdminRequestCursor(adminRequestCursor{CreatedAt: ords[limit-1], ID: cases[limit-1].ID})
	}
	for _, c := range cases {
		if c.Flags, err = queryCaseFlags(ctx, s.db, c.ID); err != nil {
			return nil, "", err
		}
	}
	return cases, next, nil
}

// GetFlagCase loads a case with its flags, or nil.
func (s *DataService) GetFlagCase(ctx context.Context, caseID string) (*flagCase, error) {
	c, err := scanFlagCase(s.db.QueryRowContext(ctx, flagCaseColumns+flagCaseFrom+` WHERE c.id = $1`, caseID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Flags, err = queryCaseFlags(ctx, s.db, caseID)
	return c, err
}

// queryCaseFlags lists a case's flags, newest first.
func queryCaseFlags(ctx context.Context, q adminQuerier, caseID string) ([]reedFlag, error) {
	rows, err := q.QueryContext(ctx, reedFlagSelect+`
		WHERE f.case_id = $1 ORDER BY f.created_at DESC, f.reporter_id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var flags []reedFlag
	for rows.Next() {
		f, err := scanReedFlag(rows)
		if err != nil {
			return nil, err
		}
		flags = append(flags, f)
	}
	return flags, rows.Err()
}

// DismissCase writes d on its case, closing it with every flag in it. A
// case already dismissed or void is refused with case-closed.
func (s *DataService) DismissCase(ctx context.Context, d *flagDismissal) error {
	return withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		return setCaseDismissalTx(ctx, tx, d, false)
	})
}

// errCaseClosed refuses acting on a case already dismissed or void.
var errCaseClosed = refuse(http.StatusConflict, "case_closed", "case-closed")

// setCaseDismissalTx writes d on its case. Recovery may write it on a case
// it voided from the withdrawals it held: a dismissal outranks that.
func setCaseDismissalTx(ctx context.Context, tx *sql.Tx, d *flagDismissal, overVoid bool) error {
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, d.Signature, d.ServerSignature)
	if err != nil {
		return err
	}
	cond := openCase
	if overVoid {
		cond = `c.dismissed_at IS NULL`
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE flag_cases c SET dismissed_by = NULLIF($2, ''), reason = $3, public_key_id = $4,
			user_signature_id = $5, server_signature_id = $6, dismissed_at = $7, voided_at = NULL
		WHERE c.id = $1 AND `+cond, d.CaseID, d.DismissedBy, d.Reason, d.Signature.ID, userSigID, serverSigID,
		d.ServerSignature.SignedAt.UTC())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = errCaseClosed
		}
		return err
	}
	return nil
}

// DeleteFlagCase deletes a case closed more than flagCaseRetention before
// now that viewerID may see, with its flags and every signature of theirs.
// It returns the case's reed.
func (s *DataService) DeleteFlagCase(ctx context.Context, caseID, viewerID string, now time.Time) (string, error) {
	var reedID string
	err := withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		var authorID string
		var closedAt sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT c.reed_id, ri.author_id, COALESCE(c.dismissed_at, c.voided_at)
			FROM flag_cases c JOIN reed_identities ri ON ri.id = c.reed_id
			WHERE c.id = $1 FOR UPDATE OF c
		`, caseID).Scan(&reedID, &authorID, &closedAt)
		if err == sql.ErrNoRows || (err == nil && authorID == viewerID) {
			return refuse(http.StatusNotFound, "no_case", "Case not found")
		}
		if err != nil {
			return err
		}
		if !closedAt.Valid {
			return refuse(http.StatusConflict, "case_open", "case-open")
		}
		if now.Sub(closedAt.Time) < flagCaseRetention {
			return refuse(http.StatusConflict, "case_too_recent", "case-too-recent")
		}
		var userSigs, serverSigs pq.Int64Array
		if err := tx.QueryRowContext(ctx, `
			SELECT
				ARRAY(SELECT x FROM (
					SELECT user_signature_id FROM flag_cases WHERE id = $1
					UNION ALL SELECT user_signature_id FROM reed_flags WHERE case_id = $1
					UNION ALL SELECT withdrawal_signature_id FROM reed_flags WHERE case_id = $1) s(x) WHERE x IS NOT NULL),
				ARRAY(SELECT x FROM (
					SELECT server_signature_id FROM flag_cases WHERE id = $1
					UNION ALL SELECT server_signature_id FROM reed_flags WHERE case_id = $1
					UNION ALL SELECT withdrawal_server_signature_id FROM reed_flags WHERE case_id = $1) s(x) WHERE x IS NOT NULL)
		`, caseID).Scan(&userSigs, &serverSigs); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM flag_cases WHERE id = $1`, caseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_signatures WHERE id = ANY($1)`, userSigs); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM server_signatures WHERE id = ANY($1)`, serverSigs)
		return err
	})
	return reedID, err
}

// ListFlags handles GET /admin/flags?state=open|dismissed|void: the cases
// the caller may see, newest first.
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
	state := query.Get("state")
	if state == "" {
		state = flagCasesOpen
	}
	cases, next, err := h.services.db.ListFlagCases(r.Context(), callerID, state, query.Get("cursor"), 50)
	if err != nil {
		h.writeAdminError(w, r, err, "Error listing flags")
		return
	}
	out := &pb.FlagCaseList{NextCursor: next}
	for _, c := range cases {
		out.Cases = append(out.Cases, pbFlagCase(c))
	}
	writeResponse(w, http.StatusOK, out)
}

// caseFrozen reports whether c can't be acted on yet: recovery brought it
// back and the server is still in lockdown.
func (h *Handlers) caseFrozen(c *flagCase) bool {
	return c.Restored && h.cfg.RecoveryMode == string(recoveryModeLockdown)
}

// DismissCase handles POST /admin/flags/cases/{caseID}/dismiss: an admin's
// signed dismissal of a case. A reason is required; it stays off the ledger.
func (h *Handlers) DismissCase(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	var msg pb.DismissCaseRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	c, err := h.services.db.GetFlagCase(r.Context(), mux.Vars(r)["caseID"])
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading case")
		return
	}
	if c == nil || c.AuthorID == callerID {
		writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	if c.Dismissal != nil || c.VoidedAt != nil {
		writeError(w, http.StatusConflict, "case-closed")
		return
	}
	if h.caseFrozen(c) {
		h.writeAdminError(w, r, errFlagFrozen, "Error dismissing case")
		return
	}
	d := &flagDismissal{
		CaseID:      c.ID,
		ReedID:      c.ReedID,
		DismissedBy: callerID,
		Reason:      strings.TrimSpace(msg.GetReason()),
		Signature:   UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
	}
	if err := h.checkFlagDismissal(r.Context(), d, msg.GetReason()); err != nil {
		h.writeAdminError(w, r, err, "Error dismissing case")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	sig, err := h.countersign(r.Context(), buildFlagDismissalServerPayload(d.CaseID, d.ReedID, h.signingKey.Fingerprint, d.Signature.Armor, now), now)
	if err != nil {
		h.writeAdminError(w, r, err, "Error countersigning dismissal")
		return
	}
	d.ServerSignature = sig
	if err := h.services.db.DismissCase(r.Context(), d); err != nil {
		h.writeAdminError(w, r, err, "Error dismissing case")
		return
	}
	h.metrics.FlagsDismissed(r.Context(), callerID, d.ReedID, len(c.Flags))
	h.reportOpenFlags(r.Context(), c.AuthorID)
	h.pushAdminRecord(r.Context(), adminRecord{Dismissal: d})
	writeResponse(w, http.StatusOK, pbFlagDismissal(d))
}

// DeleteFlagCase handles DELETE /admin/flags/cases/{caseID}: an admin
// cleans up a case closed more than six months ago, with its flags.
func (h *Handlers) DeleteFlagCase(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	caseID := mux.Vars(r)["caseID"]
	reedID, err := h.services.db.DeleteFlagCase(r.Context(), caseID, callerID, time.Now().UTC())
	if err != nil {
		h.writeAdminError(w, r, err, "Error deleting case")
		return
	}
	h.metrics.FlagCaseDeleted(r.Context(), callerID, reedID)
	h.pushAdminRecord(r.Context(), adminRecord{DeletedCase: &flagCase{ID: caseID, ReedID: reedID}})
	w.WriteHeader(http.StatusNoContent)
}

// checkFlagDismissal refuses a dismissal that isn't well formed or isn't
// signed by an active key of its admin's over the case.
func (h *Handlers) checkFlagDismissal(ctx context.Context, d *flagDismissal, rawReason string) error {
	if d.Reason == "" || d.Reason != rawReason || countCharacters(d.Reason) > maxDismissalReasonChars {
		return refuse(http.StatusBadRequest, "bad_reason", "A reason of 1 to %d characters is required", maxDismissalReasonChars)
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

// flagCaseState is what acting on a flag needs to know of its case.
type flagCaseState struct {
	Dismissed bool
	Restored  bool
}

// GetFlag loads reporterID's flag on reedID, with their withdrawal, and
// the state of its case; nil when there's none. It never carries the
// case's dismissal: this is the reporter's view.
func (s *DataService) GetFlag(ctx context.Context, reedID, reporterID string) (*reedFlag, flagCaseState, error) {
	return getFlagQ(ctx, s.db, reedID, reporterID, "")
}

func getFlagQ(ctx context.Context, q adminQuerier, reedID, reporterID, lock string) (*reedFlag, flagCaseState, error) {
	var state flagCaseState
	err := q.QueryRowContext(ctx, `
		SELECT c.dismissed_at IS NOT NULL, c.restored
		FROM reed_flags f JOIN flag_cases c ON c.id = f.case_id
		WHERE f.reed_id = $1 AND f.reporter_id = $2`+lock,
		reedID, reporterID).Scan(&state.Dismissed, &state.Restored)
	if err == sql.ErrNoRows {
		return nil, state, nil
	}
	if err != nil {
		return nil, state, err
	}
	f, err := scanReedFlag(q.QueryRowContext(ctx, reedFlagSelect+`
		WHERE f.reed_id = $1 AND f.reporter_id = $2`, reedID, reporterID))
	if err != nil {
		return nil, state, err
	}
	return &f, state, nil
}

// WithdrawFlag stores w, countersigned, on reporterID's flag on reedID,
// and answers with the flag. One already withdrawn comes back as held,
// with fresh false; one in a dismissed case is ErrFlagAcknowledged.
func (s *DataService) WithdrawFlag(ctx context.Context, reedID, reporterID string, w flagWithdrawal) (f *reedFlag, fresh bool, err error) {
	err = withAdminTx(s.db, ctx, func(tx *sql.Tx) error {
		held, state, err := getFlagQ(ctx, tx, reedID, reporterID, " FOR UPDATE OF f")
		switch {
		case err != nil:
			return err
		case held == nil:
			return refuse(http.StatusNotFound, "no_flag", "You haven't flagged that reed")
		case state.Dismissed:
			return ErrFlagAcknowledged
		case held.Withdrawal != nil:
			f = held
			return nil
		}
		if err := setFlagWithdrawalTx(ctx, tx, held.CaseID, reedID, reporterID, w); err != nil {
			return err
		}
		held.Withdrawal, f, fresh = &w, held, true
		return nil
	})
	return f, fresh, err
}

// setFlagWithdrawalTx writes w on a flag of an undismissed case, and voids
// the case once none of its flags is open.
func setFlagWithdrawalTx(ctx context.Context, tx *sql.Tx, caseID, reedID, reporterID string, w flagWithdrawal) error {
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, w.Signature, w.ServerSignature)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE reed_flags SET withdrawal_signature_id = $3, withdrawal_server_signature_id = $4
		WHERE reed_id = $1 AND reporter_id = $2
	`, reedID, reporterID, userSigID, serverSigID); err != nil {
		return err
	}
	return voidCaseIfDoneTx(ctx, tx, caseID)
}

// voidCaseIfDoneTx voids caseID, at its latest withdrawal, when it holds
// flags and every one of them is withdrawn.
func voidCaseIfDoneTx(ctx context.Context, tx *sql.Tx, caseID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE flag_cases c SET voided_at = (
			SELECT MAX(ss.signed_at) FROM reed_flags f
			JOIN server_signatures ss ON ss.id = f.withdrawal_server_signature_id
			WHERE f.case_id = c.id)
		WHERE c.id = $1 AND `+openCase+`
			AND EXISTS (SELECT 1 FROM reed_flags f WHERE f.case_id = c.id)
			AND NOT EXISTS (SELECT 1 FROM reed_flags f WHERE f.case_id = c.id AND f.withdrawal_signature_id IS NULL)
	`, caseID)
	return err
}

// OpenFlagCount counts the open flags on authorID's reeds.
func (s *DataService) OpenFlagCount(ctx context.Context, authorID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reed_flags f WHERE f.author_id = $1 AND `+openFlag, authorID).Scan(&n)
	return n, err
}

// AuthorsOpenFlaggedBy lists the authors reporterID has open flags on.
func (s *DataService) AuthorsOpenFlaggedBy(ctx context.Context, reporterID string) ([]string, error) {
	return authorsOpenFlaggedByQ(ctx, s.db, reporterID)
}

func authorsOpenFlaggedByQ(ctx context.Context, q adminQuerier, reporterID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT f.author_id FROM reed_flags f WHERE f.reporter_id = $1 AND `+openFlag, reporterID)
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

// authorsOpenFlaggedBy lists the authors reporterID has open flags on, to
// report once an account removal deletes them.
func (h *Handlers) authorsOpenFlaggedBy(ctx context.Context, reporterID string) []string {
	authors, err := h.services.db.AuthorsOpenFlaggedBy(ctx, reporterID)
	if err != nil {
		h.services.log.GetLogger(ctx).Warn().Err(err).Str("reporterID", reporterID).Msg("Failed to list flagged authors")
	}
	return authors
}

// reportOpenFlags sets the open-flags gauge of each of authors.
func (h *Handlers) reportOpenFlags(ctx context.Context, authors ...string) {
	for _, author := range authors {
		n, err := h.services.db.OpenFlagCount(ctx, author)
		if err != nil {
			h.services.log.GetLogger(ctx).Warn().Err(err).Str("authorID", author).Msg("Failed to count open flags")
			continue
		}
		h.metrics.OpenFlags(ctx, author, n)
	}
}

// ownFlagRequest reads the reed and the caller of a reporter's call on
// their own flag, or answers it.
func ownFlagRequest(w http.ResponseWriter, r *http.Request) (reedID, callerID string, ok bool) {
	callerID, _ = r.Context().Value(userIDKey).(string)
	if callerID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return "", "", false
	}
	reedID = mux.Vars(r)["reedID"]
	if _, valid := authorOf(identityID(reedID)); !valid {
		writeError(w, http.StatusBadRequest, "Invalid reed ID")
		return "", "", false
	}
	return reedID, callerID, true
}

// GetOwnFlag handles GET /flags/{reedID}: the caller's flag on that reed,
// with their withdrawal. It never says whether admins dismissed it.
func (h *Handlers) GetOwnFlag(w http.ResponseWriter, r *http.Request) {
	reedID, callerID, ok := ownFlagRequest(w, r)
	if !ok {
		return
	}
	msg := &pb.ReedFlag{ReedId: reedID, ReporterId: callerID}
	author, _ := authorOf(identityID(reedID))
	if homeServerID, foreign := h.foreignServerOf(string(author)); foreign {
		h.relayOwnFlag(w, r, homeServerID, "/api/federation/relay/flag-lookup", msg)
		return
	}
	f, _, err := h.services.db.GetFlag(r.Context(), reedID, callerID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading flag")
		return
	}
	if f == nil {
		writeError(w, http.StatusNotFound, "You haven't flagged that reed")
		return
	}
	writeResponse(w, http.StatusOK, pbReedFlag(*f))
}

// WithdrawOwnFlag handles POST /flags/{reedID}/withdraw: the caller takes
// back their flag, refused once a dismissal names it. A flag on another
// server's reed is withdrawn there.
func (h *Handlers) WithdrawOwnFlag(w http.ResponseWriter, r *http.Request) {
	reedID, callerID, ok := ownFlagRequest(w, r)
	if !ok {
		return
	}
	var body pb.FlagWithdrawal
	if err := readRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	msg := &pb.ReedFlag{ReedId: reedID, ReporterId: callerID,
		Withdrawal: &pb.FlagWithdrawal{Signature: body.GetSignature()}}
	author, _ := authorOf(identityID(reedID))
	if homeServerID, foreign := h.foreignServerOf(string(author)); foreign {
		h.relayOwnFlag(w, r, homeServerID, "/api/federation/relay/flag-withdrawal", msg)
		return
	}
	f, err := h.withdrawFlag(r.Context(), withdrawalRequestFromPB(msg), metrics.OriginLocal)
	if err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	writeResponse(w, http.StatusOK, pbReedFlag(*f))
}

// withdrawalRequestFromPB reads the reed, reporter and signature of a
// withdrawal as it travels.
func withdrawalRequestFromPB(msg *pb.ReedFlag) reedFlag {
	return reedFlag{
		ReedID:     msg.GetReedId(),
		ReporterID: msg.GetReporterId(),
		Withdrawal: &flagWithdrawal{Signature: UserSignature{
			ID:    msg.GetWithdrawal().GetSignature().GetId(),
			Armor: msg.GetWithdrawal().GetSignature().GetArmor(),
		}},
	}
}

// withdrawFlag checks req's withdrawal against its reporter's key and the
// flag held here, countersigns it and stores it, and answers with the flag.
func (h *Handlers) withdrawFlag(ctx context.Context, req reedFlag, origin metrics.Origin) (*reedFlag, error) {
	held, state, err := h.services.db.GetFlag(ctx, req.ReedID, req.ReporterID)
	switch {
	case err != nil:
		return nil, err
	case held == nil:
		return nil, refuse(http.StatusNotFound, "no_flag", "You haven't flagged that reed")
	case state.Dismissed:
		return nil, ErrFlagAcknowledged
	case held.Withdrawal != nil:
		return held, nil
	case state.Restored && h.cfg.RecoveryMode == string(recoveryModeLockdown):
		return nil, errFlagFrozen
	}
	sig := req.Withdrawal.Signature
	owner, serverID, _, ok := parseKeyFingerprint(identityID(sig.ID))
	if !ok || string(canonicalID(serverID, owner)) != req.ReporterID || sig.Armor == "" {
		return nil, refuse(http.StatusBadRequest, "bad_key", "The withdrawal isn't signed by a key of the reporter's")
	}
	key, err := h.resolvePublicKey(ctx, sig.ID)
	if err != nil {
		return nil, err
	}
	if key == nil || key.Revoked {
		return nil, refuse(http.StatusUnauthorized, "bad_key", "Active public key not available")
	}
	held.Withdrawal = &flagWithdrawal{Signature: sig}
	if err := h.services.crypto.verifySignature(ctx, string(held.withdrawalPayload()), sig.Armor, key.Armor); err != nil {
		return nil, refuse(http.StatusUnauthorized, "bad_signature", "signature verification failed")
	}
	now := time.Now().UTC().Truncate(time.Second)
	countersig, err := h.countersign(ctx, buildFlagWithdrawalServerPayload(req.ReedID, req.ReporterID, h.signingKey.Fingerprint, sig.Armor, now), now)
	if err != nil {
		return nil, err
	}
	f, fresh, err := h.services.db.WithdrawFlag(ctx, req.ReedID, req.ReporterID, flagWithdrawal{Signature: sig, ServerSignature: countersig})
	if err != nil || !fresh {
		return f, err
	}
	h.metrics.FlagWithdrawn(ctx, f.ReporterID, f.AuthorID, f.ReedID, origin)
	h.reportOpenFlags(ctx, f.AuthorID)
	h.pushAdminRecord(ctx, adminRecord{Flag: f})
	return f, nil
}

// relayOwnFlag hands a reporter's call on their flag to the reed's home
// server, one call to one peer, and answers with what it sends back.
func (h *Handlers) relayOwnFlag(w http.ResponseWriter, r *http.Request, homeServerID, path string, msg *pb.ReedFlag) {
	peer, err := h.services.db.GetServerByID(r.Context(), homeServerID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading peer")
		return
	}
	if peer == nil {
		writeError(w, http.StatusNotFound, "Reed not found")
		return
	}
	var flag pb.ReedFlag
	status, err := h.callPeerRelayEndpoint(r.Context(), homeServerID, peer.BaseURL, path, msg, &flag)
	switch {
	case err == nil && status >= 200 && status < 300:
		writeResponse(w, http.StatusOK, &flag)
	case status == http.StatusConflict:
		writeError(w, http.StatusConflict, "flag-acknowledged")
	case status == http.StatusNotFound:
		writeError(w, http.StatusNotFound, "You haven't flagged that reed")
	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		writeError(w, status, "The reed's server refused the withdrawal")
	default:
		h.services.log.GetLogger(r.Context()).Warn().Err(err).Int("status", status).Str("peerServerID", homeServerID).Str("path", path).Msg("Peer did not answer a flag call")
		writeError(w, http.StatusBadGateway, "Couldn't reach the reed's server")
	}
}

// peerReporterFlag reads a ReedFlag a peer sent about one of its members'
// flags on a reed authored here, or answers it.
func (h *Handlers) peerReporterFlag(w http.ResponseWriter, r *http.Request, leg string) (*pb.ReedFlag, string, bool) {
	peerServerID, _ := r.Context().Value(peerServerIDKey).(string)
	if peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return nil, "", false
	}
	var msg pb.ReedFlag
	if err := readRequest(r, &msg); err != nil {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, leg, false)
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return nil, "", false
	}
	if _, serverID, ok := parseIdentityID(identityID(msg.GetReporterId())); !ok || serverID != peerServerID {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, leg, false)
		writeError(w, http.StatusBadRequest, "The reporter isn't a member of the calling peer")
		return nil, "", false
	}
	if _, ok := authorOf(identityID(msg.GetReedId())); !ok {
		h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, leg, false)
		writeError(w, http.StatusBadRequest, "Invalid reed ID")
		return nil, "", false
	}
	return &msg, peerServerID, true
}

// FlagLookupFromPeer answers a peer with one of its members' flags on a
// reed authored here, with their withdrawal, never saying if it was dismissed.
func (h *Handlers) FlagLookupFromPeer(w http.ResponseWriter, r *http.Request) {
	msg, peerServerID, ok := h.peerReporterFlag(w, r, "flag-lookup")
	if !ok {
		return
	}
	f, _, err := h.services.db.GetFlag(r.Context(), msg.GetReedId(), msg.GetReporterId())
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "flag-lookup", err == nil && f != nil)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading flag")
		return
	}
	if f == nil {
		writeError(w, http.StatusNotFound, "No such flag")
		return
	}
	writeResponse(w, http.StatusOK, pbReedFlag(*f))
}

// FlagWithdrawalFromPeer takes a withdrawal one of the calling peer's
// members signed of their flag on a reed authored here.
func (h *Handlers) FlagWithdrawalFromPeer(w http.ResponseWriter, r *http.Request) {
	msg, peerServerID, ok := h.peerReporterFlag(w, r, "flag-withdrawal")
	if !ok {
		return
	}
	f, err := h.withdrawFlag(r.Context(), withdrawalRequestFromPB(msg), metrics.OriginPeer)
	h.metrics.FederationRelay(r.Context(), metrics.DirectionIn, peerServerID, "flag-withdrawal", err == nil)
	if err != nil {
		h.writeFlagError(w, r, err)
		return
	}
	writeResponse(w, http.StatusOK, pbReedFlag(*f))
}
