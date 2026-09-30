//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// ErrVouchConflict is returned when an existing live vouch differs from
// the cert being inserted; an identical replay succeeds.
var ErrVouchConflict = errors.New("vouch conflict")

// ErrVouchNotFound is returned when withdrawing a vouch that isn't there.
var ErrVouchNotFound = errors.New("vouch not found")

// ErrVouchInvalidID is returned when a vouch id is not the canonical
// voucherID@serverID/UUIDv7 form.
var ErrVouchInvalidID = errors.New("invalid vouch id")

// vouchRow is one user_vouches row before its signatures are resolved.
type vouchRow struct {
	ID              string
	VoucherUserID   string
	VoucherKeyID    string
	SubjectUserID   string
	SubjectKeyID    string
	Note            string
	UserSignatureID int64
	ServerSigID     int64
	CreatedAt       time.Time
	WithdrawalSigID sql.NullInt64
	WithdrawalSrvID sql.NullInt64
}

const vouchSelectColumns = `
	uv.id, uv.voucher_user_id, uv.voucher_key_id, uv.subject_user_id,
	uv.subject_key_id, uv.note, uv.user_signature_id, uv.server_signature_id,
	uv.created_at, uv.withdrawal_signature_id,
	uv.withdrawal_server_signature_id`

func scanVouchRow(scan func(...any) error) (*vouchRow, error) {
	var r vouchRow
	if err := scan(
		&r.ID, &r.VoucherUserID, &r.VoucherKeyID, &r.SubjectUserID, &r.SubjectKeyID,
		&r.Note, &r.UserSignatureID, &r.ServerSigID, &r.CreatedAt,
		&r.WithdrawalSigID, &r.WithdrawalSrvID,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// hydrateVouch turns a row into the wire cert, resolving both signature
// blocks and the withdrawal signature when present.
func (s *DataService) hydrateVouch(ctx context.Context, q signingDBTX, r *vouchRow) (*VouchCert, error) {
	userSig, err := getUserSignatureWire(ctx, q, r.UserSignatureID)
	if err != nil {
		return nil, err
	}
	serverSig, err := getServerSignatureWire(ctx, q, r.ServerSigID)
	if err != nil {
		return nil, err
	}
	cert := &VouchCert{
		ID:              r.ID,
		VoucherUserID:   r.VoucherUserID,
		VoucherKeyID:    r.VoucherKeyID,
		SubjectUserID:   r.SubjectUserID,
		SubjectKeyID:    r.SubjectKeyID,
		Note:            r.Note,
		UserSignature:   userSig,
		ServerSignature: serverSig,
	}
	// Written in one transaction: the withdrawal is whole or absent.
	if r.WithdrawalSigID.Valid && r.WithdrawalSrvID.Valid {
		userSig, err := getUserSignatureWire(ctx, q, r.WithdrawalSigID.Int64)
		if err != nil {
			return nil, err
		}
		srvSig, err := getServerSignatureWire(ctx, q, r.WithdrawalSrvID.Int64)
		if err != nil {
			return nil, err
		}
		cert.Withdrawal = &VouchWithdrawal{UserSignature: userSig, ServerSignature: srvSig}
	}
	return cert, nil
}

// GetVouch returns the live vouch for (voucherID, subjectKeyID), or nil.
// Withdrawn rows for the same pair are reachable by id only.
func (s *DataService) GetVouch(ctx context.Context, voucherID, subjectKeyID string) (*VouchCert, error) {
	row, err := scanVouchRow(s.db.QueryRowContext(ctx, `
		SELECT `+vouchSelectColumns+`
		FROM user_vouches uv
		JOIN user_vouches_active a ON a.vouch_id = uv.id
		WHERE a.voucher_user_id = $1 AND a.subject_key_id = $2
	`, voucherID, subjectKeyID).Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateVouch(ctx, s.db, row)
}

// InsertVouch stores a vouch cert once. An identical replay is a no-op, a
// differing signature for the same pair is ErrVouchConflict, and
// re-vouching a withdrawn row revives it with the new signature.
func (s *DataService) InsertVouch(ctx context.Context, cert VouchCert) error {
	if reason := validateVouchID(cert.ID, cert.VoucherUserID, s.serverID); reason != "" {
		return fmt.Errorf("%w: %s", ErrVouchInvalidID, reason)
	}
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Only a live row can conflict; history may hold withdrawn rows for
	// the same pair.
	existing, err := scanVouchRow(tx.QueryRowContext(ctx, `
		SELECT `+vouchSelectColumns+`
		FROM user_vouches uv
		JOIN user_vouches_active a ON a.vouch_id = uv.id
		WHERE a.voucher_user_id = $1 AND a.subject_key_id = $2
		FOR UPDATE OF uv
	`, cert.VoucherUserID, cert.SubjectKeyID).Scan)

	switch {
	case err == sql.ErrNoRows:
		if err := insertVouchRow(ctx, tx, cert); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		// A live vouch stands: identical replay is idempotent, anything
		// else conflicts.
		live, err := s.hydrateVouch(ctx, tx, existing)
		if err != nil {
			return err
		}
		if !sameVouchSignature(live, cert) {
			return ErrVouchConflict
		}
	}

	return tx.Commit()
}

// sameVouchSignature reports whether a replay carries the identical signed
// material, which is what makes re-posting idempotent.
func sameVouchSignature(stored *VouchCert, incoming VouchCert) bool {
	return stored.UserSignature.Armor == incoming.UserSignature.Armor &&
		stored.UserSignature.ID == incoming.UserSignature.ID &&
		stored.Note == incoming.Note
}

func insertVouchRow(ctx context.Context, tx *sql.Tx, cert VouchCert) error {
	userSigID, err := insertUserSignature(ctx, tx, cert.UserSignature.ID, cert.UserSignature.Armor)
	if err != nil {
		return err
	}
	serverSigID, err := insertServerSignature(
		ctx, tx,
		cert.ServerSignature.ID, cert.ServerSignature.Armor, cert.ServerSignature.SignedAt,
	)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_vouches (
			id, voucher_user_id, voucher_key_id, subject_user_id, subject_key_id,
			note, user_signature_id, server_signature_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		cert.ID, cert.VoucherUserID, cert.VoucherKeyID, cert.SubjectUserID,
		cert.SubjectKeyID, cert.Note, userSigID, serverSigID,
		cert.ServerSignature.SignedAt,
	); err != nil {
		return fmt.Errorf("insert user vouch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_vouches_active (
			vouch_id, voucher_user_id, subject_user_id, subject_key_id
		) VALUES ($1, $2, $3, $4)
	`, cert.ID, cert.VoucherUserID, cert.SubjectUserID, cert.SubjectKeyID); err != nil {
		return fmt.Errorf("activate user vouch: %w", err)
	}
	return nil
}

// WithdrawVouch records a signed retraction and drops the row from the
// active set. History keeps it: a client that cached the vouch needs signed
// evidence of the retraction, not the server's word.
func (s *DataService) WithdrawVouch(
	ctx context.Context,
	voucherID, subjectKeyID string,
	withdrawal UserSignature,
	withdrawalServer ServerSignature,
) (*VouchCert, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var vouchID string
	err = tx.QueryRowContext(ctx, `
		SELECT uv.id
		FROM user_vouches uv
		JOIN user_vouches_active a ON a.vouch_id = uv.id
		WHERE a.voucher_user_id = $1 AND a.subject_key_id = $2
		FOR UPDATE OF uv
	`, voucherID, subjectKeyID).Scan(&vouchID)
	if err == sql.ErrNoRows {
		return nil, ErrVouchNotFound
	}
	if err != nil {
		return nil, err
	}

	userSigID, err := insertUserSignature(ctx, tx, withdrawal.ID, withdrawal.Armor)
	if err != nil {
		return nil, err
	}
	serverSigID, err := insertServerSignature(
		ctx, tx,
		withdrawalServer.ID, withdrawalServer.Armor, withdrawalServer.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_vouches
		SET withdrawal_signature_id = $2, withdrawal_server_signature_id = $3
		WHERE id = $1
	`, vouchID, userSigID, serverSigID); err != nil {
		return nil, fmt.Errorf("withdraw user vouch: %w", err)
	}
	// Dropping the active row is what takes it out of every list read.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_vouches_active WHERE vouch_id = $1
	`, vouchID); err != nil {
		return nil, fmt.Errorf("deactivate user vouch: %w", err)
	}

	row, err := scanVouchRow(tx.QueryRowContext(ctx, `
		SELECT `+vouchSelectColumns+`
		FROM user_vouches uv
		WHERE uv.id = $1
	`, vouchID).Scan)
	if err != nil {
		return nil, err
	}
	cert, err := s.hydrateVouch(ctx, tx, row)
	if err != nil {
		return nil, err
	}
	return cert, tx.Commit()
}

// LastVouchCreatedAt returns when this voucher last vouched for this key,
// live or withdrawn. Zero time when never.
func (s *DataService) LastVouchCreatedAt(
	ctx context.Context, voucherID, subjectKeyID string,
) (time.Time, error) {
	var createdAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT created_at FROM user_vouches
		WHERE voucher_user_id = $1 AND subject_key_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, voucherID, subjectKeyID).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return createdAt.UTC(), nil
}

// vouchCursor pages by the server countersignature time, then key id to
// break ties. Server time is used because it is the one timestamp in the
// record the caller's own client did not choose.
type vouchCursor struct {
	SignedAt     time.Time `json:"s"`
	SubjectKeyID string    `json:"k"`
}

func encodeVouchCursor(c vouchCursor) string {
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeVouchCursor(s string) (*vouchCursor, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	var c vouchCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor payload: %w", err)
	}
	return &c, nil
}

// ListVouchesForSubject pages the live vouches naming this user, newest
// first by server countersignature time.
func (s *DataService) ListVouchesForSubject(
	ctx context.Context, userID string, limit int, cursor string,
) (*VouchListResponse, error) {
	return s.listVouches(ctx, "subject_user_id", userID, false, limit, cursor)
}

// ListVouchesByVoucher pages every vouch this user made, withdrawn ones
// included, which is what the caller audits their own signing history with.
func (s *DataService) ListVouchesByVoucher(
	ctx context.Context, userID string, limit int, cursor string,
) (*VouchListResponse, error) {
	return s.listVouches(ctx, "voucher_user_id", userID, true, limit, cursor)
}

func (s *DataService) listVouches(
	ctx context.Context,
	column string,
	userID string,
	includeWithdrawn bool,
	limit int,
	cursor string,
) (*VouchListResponse, error) {
	args := []any{userID}
	query := `
		SELECT ` + vouchSelectColumns + `
		FROM user_vouches uv
		JOIN server_signatures ss ON ss.id = uv.server_signature_id`
	// Live-only reads join the active set; the audit read walks history.
	if !includeWithdrawn {
		query += `
		JOIN user_vouches_active a ON a.vouch_id = uv.id`
	}
	query += `
		WHERE uv.` + column + ` = $1`
	if cursor != "" {
		c, err := decodeVouchCursor(cursor)
		if err != nil {
			return nil, err
		}
		args = append(args, c.SignedAt.UTC().Truncate(time.Second), c.SubjectKeyID)
		query += fmt.Sprintf(
			` AND (ss.signed_at, uv.subject_key_id) < ($%d, $%d)`,
			len(args)-1, len(args),
		)
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY ss.signed_at DESC, uv.subject_key_id DESC
		LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list user vouches: %w", err)
	}
	defer rows.Close()

	var pending []*vouchRow
	for rows.Next() {
		row, err := scanVouchRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(pending) > limit
	if hasMore {
		pending = pending[:limit]
	}

	out := &VouchListResponse{Vouches: make([]VouchCert, 0, len(pending))}
	for _, row := range pending {
		cert, err := s.hydrateVouch(ctx, s.db, row)
		if err != nil {
			return nil, err
		}
		out.Vouches = append(out.Vouches, *cert)
	}
	if hasMore && len(out.Vouches) > 0 {
		last := out.Vouches[len(out.Vouches)-1]
		out.NextCursor = encodeVouchCursor(vouchCursor{
			SignedAt:     last.ServerSignature.SignedAt,
			SubjectKeyID: last.SubjectKeyID,
		})
	}
	return out, nil
}

// CreateVouch handles POST /vouches — a signed attestation that the caller
// compared fingerprints with the subject out of band.
func (h *Handlers) CreateVouch(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("CreateVouch request received")

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	voucherID, ok := h.resolveActingUser(r, values.Get("voucherID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}
	subjectKeyID := strings.TrimSpace(values.Get("subjectKeyID"))
	voucherKeyID := strings.TrimSpace(values.Get("voucherKeyID"))
	userSignatureB64 := strings.TrimSpace(values.Get("signature"))
	note := values.Get("note")

	switch {
	case subjectKeyID == "":
		writeResponse(w, http.StatusBadRequest, "Argument `subjectKeyID` is required")
		return
	case voucherKeyID == "":
		writeResponse(w, http.StatusBadRequest, "Argument `voucherKeyID` is required")
		return
	case userSignatureB64 == "":
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	if utf8.RuneCountInString(note) > MaxVouchNoteChars {
		log.Error().Str("voucherID", voucherID).Msg("Vouch note too long")
		writeResponse(w, http.StatusBadRequest, "Note cannot exceed 140 characters")
		return
	}

	// The subject is whoever owns the key being vouched for; nothing in
	// the request names them separately.
	subjectIdentity, ok := authorOf(identityID(subjectKeyID))
	if !ok {
		writeResponse(w, http.StatusBadRequest, "`subjectKeyID` is not a canonical key id")
		return
	}
	subjectUserID := string(subjectIdentity)

	// Self-vouching asserts nothing: the caller would be attesting to a key
	// they already control.
	if subjectUserID == voucherID {
		writeResponse(w, http.StatusBadRequest, "Cannot vouch for yourself")
		return
	}

	subjectKey, err := h.resolvePublicKey(r.Context(), subjectKeyID)
	if err != nil {
		log.Error().Str("subjectKeyID", subjectKeyID).Err(err).Msg("Error loading subject key")
		internalServerError(w)
		return
	}
	if subjectKey == nil {
		writeResponse(w, http.StatusNotFound, "Subject key not found")
		return
	}
	// A vouch for an already-revoked key would be stale on arrival.
	if subjectKey.Revoked {
		writeResponse(w, http.StatusConflict, "Subject key is revoked")
		return
	}

	existing, err := h.services.db.GetVouch(r.Context(), voucherID, subjectKeyID)
	if err != nil {
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error loading vouch")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature.Armor != userSignatureB64 || existing.Note != note {
			writeResponse(w, http.StatusConflict, "Vouch already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, existing)
		return
	}

	// History is append-only, so an unbounded withdraw/re-vouch cycle would
	// grow the table without limit. One per key per day is far above any
	// honest rate: verification happens in person.
	lastAt, err := h.services.db.LastVouchCreatedAt(r.Context(), voucherID, subjectKeyID)
	if err != nil {
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error loading last vouch time")
		internalServerError(w)
		return
	}
	if !lastAt.IsZero() {
		if wait := vouchCooldown - time.Since(lastAt); wait > 0 {
			log.Info().
				Str("voucherID", voucherID).
				Str("subjectKeyID", subjectKeyID).
				Dur("retryAfter", wait).
				Msg("Vouch rejected: cooldown")
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeResponse(w, http.StatusTooManyRequests,
				"You can verify this key again later")
			return
		}
	}

	// The voucher's key need only be able to sign. Its revocation state is
	// not checked, because revoking it does not retract their vouches.
	voucherKey, err := h.resolvePublicKey(r.Context(), voucherKeyID)
	if err != nil {
		log.Error().Str("voucherKeyID", voucherKeyID).Err(err).Msg("Error loading voucher key")
		internalServerError(w)
		return
	}
	if voucherKey == nil {
		writeResponse(w, http.StatusNotFound, "Voucher key not found")
		return
	}
	if !keyBelongsTo(voucherKeyID, voucherID) {
		writeResponse(w, http.StatusBadRequest, "`voucherKeyID` is not owned by the caller")
		return
	}

	userSigArmor, err := base64Decode(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	userPayload := buildVouchUserPayload(voucherKeyID, subjectKeyID, note)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, voucherKey.Armor); err != nil {
		log.Error().
			Str("voucherID", voucherID).
			Str("subjectKeyID", subjectKeyID).
			Err(err).
			Msg("vouch signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildVouchServerPayload(
		subjectKeyID, h.signingKey.Fingerprint, userSignatureB64, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing vouch countersignature")
		internalServerError(w)
		return
	}

	// Each attestation gets its own id, so the withdrawal a re-vouch
	// replaces stays fetchable and verifiable.
	vouchUUID, err := uuid.NewV7()
	if err != nil {
		log.Error().Err(err).Msg("Error minting vouch id")
		internalServerError(w)
		return
	}
	vouchID := string(appendEntity(identityID(voucherID), vouchUUID.String()))

	cert := VouchCert{
		ID:              vouchID,
		VoucherUserID:   voucherID,
		VoucherKeyID:    voucherKeyID,
		SubjectUserID:   subjectUserID,
		SubjectKeyID:    subjectKeyID,
		Note:            note,
		UserSignature:   UserSignature{ID: voucherKeyID, Armor: userSignatureB64},
		ServerSignature: serverSignature,
	}
	if err := h.services.db.InsertVouch(r.Context(), cert); err != nil {
		if errors.Is(err, ErrVouchConflict) {
			writeResponse(w, http.StatusConflict, "Vouch already exists with a different signature")
			return
		}
		if errors.Is(err, ErrVouchInvalidID) {
			log.Error().Str("vouchID", cert.ID).Err(err).Msg("Invalid vouch id")
			writeResponse(w, http.StatusBadRequest, "Invalid vouch id")
			return
		}
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error storing vouch")
		internalServerError(w)
		return
	}

	// Tell the subject if they are online; otherwise they reconcile later.
	h.broadcastChan <- realtimeBroadcastMessage{
		Type:    realtimeVouchCreated,
		UserID:  subjectUserID,
		VouchID: cert.ID,
	}

	log.Info().
		Str("voucherID", voucherID).
		Str("subjectKeyID", subjectKeyID).
		Msg("Vouch accepted")
	writeResponse(w, http.StatusOK, cert)
}

// WithdrawVouch handles DELETE /vouches/{subjectKeyID}. Signed, unlike
// unliking: a retraction is a security claim, so a client that cached the
// vouch gets signed evidence rather than the server's word.
func (h *Handlers) WithdrawVouch(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("WithdrawVouch request received")

	subjectKeyID := mux.Vars(r)["subjectKeyID"]
	if subjectKeyID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `subjectKeyID` is required")
		return
	}

	// r.FormValue skips DELETE bodies, so read it directly.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	values, _ := url.ParseQuery(string(body))

	voucherID, ok := h.resolveActingUser(r, values.Get("voucherID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}
	voucherKeyID := strings.TrimSpace(values.Get("voucherKeyID"))
	signatureB64 := strings.TrimSpace(values.Get("signature"))
	if voucherKeyID == "" || signatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `voucherKeyID` and `signature` are required")
		return
	}

	existing, err := h.services.db.GetVouch(r.Context(), voucherID, subjectKeyID)
	if err != nil {
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error loading vouch")
		internalServerError(w)
		return
	}
	if existing == nil {
		writeResponse(w, http.StatusNotFound, "Vouch not found")
		return
	}

	// Verified against the voucher's key as named now, not the key that
	// signed the original vouch: a user who rotated must still retract.
	if !keyBelongsTo(voucherKeyID, voucherID) {
		writeResponse(w, http.StatusBadRequest, "`voucherKeyID` is not owned by the caller")
		return
	}
	voucherKey, err := h.resolvePublicKey(r.Context(), voucherKeyID)
	if err != nil {
		log.Error().Str("voucherKeyID", voucherKeyID).Err(err).Msg("Error loading voucher key")
		internalServerError(w)
		return
	}
	if voucherKey == nil {
		writeResponse(w, http.StatusNotFound, "Voucher key not found")
		return
	}

	sigArmor, err := base64Decode(signatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	payload := buildVouchWithdrawalUserPayload(existing.ID)
	if err := h.services.crypto.verifySignature(string(payload), sigArmor, voucherKey.Armor); err != nil {
		log.Error().
			Str("voucherID", voucherID).
			Str("subjectKeyID", subjectKeyID).
			Err(err).
			Msg("withdrawal signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// One truncated instant for both: signing over a value other than the
	// stored one would never verify.
	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildVouchWithdrawalServerPayload(
		existing.ID, h.signingKey.Fingerprint, signatureB64, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing withdrawal countersignature")
		internalServerError(w)
		return
	}

	cert, err := h.services.db.WithdrawVouch(
		r.Context(), voucherID, subjectKeyID,
		UserSignature{ID: voucherKeyID, Armor: signatureB64},
		serverSignature,
	)
	if err != nil {
		if errors.Is(err, ErrVouchNotFound) {
			writeResponse(w, http.StatusNotFound, "Vouch not found")
			return
		}
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error withdrawing vouch")
		internalServerError(w)
		return
	}

	log.Info().
		Str("voucherID", voucherID).
		Str("subjectKeyID", subjectKeyID).
		Msg("Vouch withdrawal accepted")
	writeResponse(w, http.StatusOK, cert)
}

// isVouchIDWellFormed reports whether an id has the canonical
// voucherID@serverID/UUIDv7 shape, without asserting who owns it.
func isVouchIDWellFormed(vouchID string) bool {
	_, _, entity, ok := parseKeyFingerprint(identityID(vouchID))
	return ok && isValidUUIDv7(entity)
}

// validateVouchID checks a vouch id is the canonical
// voucherID@serverID/UUIDv7 form, owned by the named voucher and minted on
// this server. Returns the reason it failed, or "" when valid.
func validateVouchID(vouchID, voucherID, serverID string) string {
	ownerID, embeddedServerID, entity, ok := parseKeyFingerprint(identityID(vouchID))
	if !ok {
		return "vouch id is not in voucherID@serverID/uuid form"
	}
	if string(canonicalID(embeddedServerID, ownerID)) != voucherID {
		return "vouch id is not owned by the voucher"
	}
	if embeddedServerID != serverID {
		return "vouch id was not minted by this server"
	}
	if !isValidUUIDv7(entity) {
		return "vouch id entity is not a UUIDv7"
	}
	return ""
}

// keyBelongsTo reports whether a canonical key id is owner-prefixed by the
// given canonical user id, without splitting either apart.
func keyBelongsTo(keyID, userID string) bool {
	return strings.HasPrefix(keyID, userID+"/")
}

// VouchListResponse is the wire shape of every vouch list read.
type VouchListResponse struct {
	Vouches    []VouchCert `json:"vouches"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// vouchCooldown bounds withdraw/re-vouch churn.
const vouchCooldown = 24 * time.Hour

const vouchPageDefault = 50
const vouchPageMax = 100

func vouchPageLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return vouchPageDefault
	}
	if limit > vouchPageMax {
		return vouchPageMax
	}
	return limit
}

// ListVouchesForUser handles GET /users/{userID}/vouches — who vouched for
// this user. Public: trust evidence is useless if you must already be
// signed in to read it.
func (h *Handlers) ListVouchesForUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}
	limit := vouchPageLimit(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")

	list, err := h.services.db.ListVouchesForSubject(r.Context(), userID, limit, cursor)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error listing vouches")
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, list)
}

// ListMyVouches handles GET /vouches — every vouch the caller made,
// withdrawn ones included, so they can audit what their keys signed.
func (h *Handlers) ListMyVouches(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	voucherID, ok := r.Context().Value(userIDKey).(string)
	if !ok || voucherID == "" {
		writeResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	limit := vouchPageLimit(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")

	list, err := h.services.db.ListVouchesByVoucher(r.Context(), voucherID, limit, cursor)
	if err != nil {
		log.Error().Str("voucherID", voucherID).Err(err).Msg("Error listing own vouches")
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, list)
}

// GetVouchByID loads one vouch by its own canonical id, live or withdrawn.
func (s *DataService) GetVouchByID(ctx context.Context, vouchID string) (*VouchCert, error) {
	row, err := scanVouchRow(s.db.QueryRowContext(ctx, `
		SELECT `+vouchSelectColumns+`
		FROM user_vouches uv
		WHERE uv.id = $1
	`, vouchID).Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateVouch(ctx, s.db, row)
}

// ListVouchIDsForSubject returns the ids of the live vouches naming this
// user, which is all /info carries: ids are cheap to ship and worthless to
// forge, since no mark appears until the client verifies the cert behind one.
func (s *DataService) ListVouchIDsForSubject(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT vouch_id FROM user_vouches_active
		WHERE subject_user_id = $1
		ORDER BY vouch_id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list vouch ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetVouch handles GET /users/{userID}/vouches/{vouchID}. The two params are
// different identities: {userID} is the subject, {vouchID} is the vouch's own
// id owned by the voucher. Both are used whole, never recomposed.
func (h *Handlers) GetVouch(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	subjectUserID := mux.Vars(r)["userID"]
	vouchID := mux.Vars(r)["vouchID"]
	if subjectUserID == "" || vouchID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `vouchID` are required")
		return
	}

	// Reject a malformed id before touching the DB: a well-formed id is
	// the only thing that can name a vouch this server minted.
	if !isVouchIDWellFormed(vouchID) {
		writeResponse(w, http.StatusBadRequest, "`vouchID` is not a canonical vouch id")
		return
	}

	cert, err := h.services.db.GetVouchByID(r.Context(), vouchID)
	if err != nil {
		log.Error().Str("vouchID", vouchID).Err(err).Msg("Error loading vouch")
		internalServerError(w)
		return
	}
	// A vouch is never served under a subject it says nothing about.
	if cert == nil || cert.SubjectUserID != subjectUserID {
		writeResponse(w, http.StatusNotFound, "Vouch not found")
		return
	}

	writeResponse(w, http.StatusOK, cert)
}
