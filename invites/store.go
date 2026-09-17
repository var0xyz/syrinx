package invites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"syrinx/identity"
	"syrinx/signing"

	"github.com/lib/pq"
)

// Invite is the durable invite row (never includes the raw token).
// CreatedBy/ClaimedBy hold the full "userID@serverID" form; ClaimedBy is
// exposed via statusResponse.ClaimedBy on GET /api/invites/{id}.
// UserSignature is the inviter's attestation over the invite fields.
type Invite struct {
	ID            string
	CreatedBy     string
	CreatedAt     time.Time
	GrantedRole   string
	ClaimedAt     *time.Time
	ClaimedBy     *string
	RevokedAt     *time.Time
	UserSignature signing.UserSignature
}

// Store persists invites. MarkClaimed accepts an existing *sql.Tx for signup.
// ServerID builds the "userID@serverID" form for invites.created_by/claimed_by;
// most callers pass that form already, but MarkClaimed/GetPendingInvite take bare userIDs.
type Store struct {
	DB       *sql.DB
	ServerID string
}

func (s *Store) CountByCreator(ctx context.Context, creatorID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM invites WHERE created_by = $1
	`, creatorID).Scan(&n)
	return n, err
}

func (s *Store) Insert(
	ctx context.Context,
	id, creatorID string,
	tokenHash []byte,
	createdAt time.Time,
	grantedRole string,
	userKeyID, userSignatureArmor string,
) error {
	if grantedRole == "" {
		grantedRole = "user"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userSignatureID, err := signing.InsertUserSignature(ctx, tx, userKeyID, userSignatureArmor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO invites (id, created_by, token_hash, created_at, granted_role, user_signature_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, creatorID, tokenHash, createdAt.UTC(), grantedRole, userSignatureID)
	if isUniqueViolation(err) {
		return ErrInviteExists
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetByID(ctx context.Context, id string) (*Invite, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE id = $1
	`, id)
	return scanInvite(ctx, s.DB, row)
}

func (s *Store) GetByTokenHash(ctx context.Context, hash []byte) (*Invite, error) {
	return getByTokenHash(ctx, s.DB, s.DB, hash)
}

func (s *Store) GetByTokenHashTx(ctx context.Context, tx *sql.Tx, hash []byte) (*Invite, error) {
	return getByTokenHash(ctx, tx, tx, hash)
}

type tokenHashQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getByTokenHash has no creatorID in scope (token_hash is globally unique) —
// created_by/claimed_by come back in full form from the row itself, no
// conversion needed on the query side here.
func getByTokenHash(ctx context.Context, q tokenHashQuerier, sigDB signing.DBTX, hash []byte) (*Invite, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE token_hash = $1
	`, hash)
	return scanInvite(ctx, sigDB, row)
}

func (s *Store) GetPendingInvite(ctx context.Context, id string, hash []byte) (*Invite, error) {
	return getPendingInvite(ctx, s.DB, s.DB, id, hash)
}

func (s *Store) GetPendingInviteTx(ctx context.Context, tx *sql.Tx, id string, hash []byte) (*Invite, error) {
	return getPendingInvite(ctx, tx, tx, id, hash)
}

func getPendingInvite(ctx context.Context, q tokenHashQuerier, sigDB signing.DBTX, id string, hash []byte) (*Invite, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE id = $1 AND token_hash = $2
	`, id, hash)
	inv, err := scanInvite(ctx, sigDB, row)
	if err != nil || inv == nil {
		return inv, err
	}
	if inv.Status() != "pending" {
		return nil, nil
	}
	return inv, nil
}

// MarkClaimed claims an unused, unrevoked invite inside tx. inviteID is
// canonical; claimedBy is a bare userID. Returns whether a row was updated.
func (s *Store) MarkClaimed(
	ctx context.Context,
	tx *sql.Tx,
	inviteID, claimedBy string,
	claimedAt time.Time,
) (bool, error) {
	claimedByIdentity := identity.CanonicalID(s.ServerID, claimedBy)
	res, err := tx.ExecContext(ctx, `
		UPDATE invites
		SET claimed_at = $2, claimed_by = $3
		WHERE id = $1
		  AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, claimedAt.UTC(), claimedByIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Revoke marks an unused invite revoked. inviteID is canonical; callerID
// must match the invite's creator.
func (s *Store) Revoke(
	ctx context.Context,
	inviteID, callerID string,
	revokedAt time.Time,
) error {
	var createdBy string
	var claimedAt, existingRevoked sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT created_by, claimed_at, revoked_at
		FROM invites WHERE id = $1
	`, inviteID).Scan(&createdBy, &claimedAt, &existingRevoked)
	if err == sql.ErrNoRows {
		return ErrInviteNotFound
	}
	if err != nil {
		return err
	}
	if createdBy != callerID {
		return ErrInviteNotOwner
	}
	if claimedAt.Valid {
		return ErrInviteAlreadyClaimed
	}
	if existingRevoked.Valid {
		return ErrInviteAlreadyRevoked
	}

	res, err := s.DB.ExecContext(ctx, `
		UPDATE invites
		SET revoked_at = $2
		WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, revokedAt.UTC())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInviteNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

type scannable interface {
	Scan(dest ...any) error
}

// scanInvite scans created_by/claimed_by as identity.IdentityID (the row's
// actual stored form) and keeps that form on Invite's wire-facing fields, no
// decode to bare. Loads the inviter's persisted signature via sigDB.
func scanInvite(ctx context.Context, sigDB signing.DBTX, row scannable) (*Invite, error) {
	var inv Invite
	var createdBy identity.IdentityID
	var claimedAt, revokedAt sql.NullTime
	var claimedBy sql.NullString
	var userSignatureID int64
	err := row.Scan(
		&inv.ID,
		&createdBy,
		&inv.CreatedAt,
		&inv.GrantedRole,
		&claimedAt,
		&claimedBy,
		&revokedAt,
		&userSignatureID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inv.CreatedBy = string(createdBy)
	if claimedAt.Valid {
		t := claimedAt.Time.UTC()
		inv.ClaimedAt = &t
	}
	if claimedBy.Valid {
		s := claimedBy.String
		inv.ClaimedBy = &s
	}
	if revokedAt.Valid {
		t := revokedAt.Time.UTC()
		inv.RevokedAt = &t
	}
	sigRow, err := signing.GetUserSignature(ctx, sigDB, userSignatureID)
	if err != nil {
		return nil, err
	}
	inv.UserSignature = *sigRow
	return &inv, nil
}
