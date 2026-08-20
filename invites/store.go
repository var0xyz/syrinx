package invites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"syrinx/identity"

	"github.com/lib/pq"
)

// Invite is the durable invite row (never includes the raw token).
// CreatedBy/ClaimedBy hold the full "userID@serverID" form; ClaimedBy is
// exposed via statusResponse.ClaimedBy on GET /api/invites/{id}.
type Invite struct {
	ID          string
	CreatedBy   string
	CreatedAt   time.Time
	GrantedRole string
	ClaimedAt   *time.Time
	ClaimedBy   *string
	RevokedAt   *time.Time
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
) error {
	if grantedRole == "" {
		grantedRole = "user"
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO invites (created_by, id, token_hash, created_at, granted_role)
		VALUES ($1, $2, $3, $4, $5)
	`, creatorID, id, tokenHash, createdAt.UTC(), grantedRole)
	if isUniqueViolation(err) {
		return ErrInviteExists
	}
	return err
}

func (s *Store) GetByCreatorAndID(ctx context.Context, creatorID, id string) (*Invite, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at
		FROM invites
		WHERE created_by = $1 AND id = $2
	`, creatorID, id)
	inv, err := scanInvite(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Store) GetByTokenHash(ctx context.Context, hash []byte) (*Invite, error) {
	return getByTokenHash(ctx, s.DB, hash)
}

func (s *Store) GetByTokenHashTx(ctx context.Context, tx *sql.Tx, hash []byte) (*Invite, error) {
	return getByTokenHash(ctx, tx, hash)
}

type tokenHashQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getByTokenHash has no creatorID in scope (token_hash is globally unique) —
// created_by/claimed_by come back in full form from the row itself, no
// conversion needed on the query side here.
func getByTokenHash(ctx context.Context, q tokenHashQuerier, hash []byte) (*Invite, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at
		FROM invites
		WHERE token_hash = $1
	`, hash)
	inv, err := scanInvite(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Store) GetPendingInvite(ctx context.Context, creatorID, id string, hash []byte) (*Invite, error) {
	return getPendingInvite(ctx, s.DB, s.ServerID, creatorID, id, hash)
}

func (s *Store) GetPendingInviteTx(ctx context.Context, tx *sql.Tx, creatorID, id string, hash []byte) (*Invite, error) {
	return getPendingInvite(ctx, tx, s.ServerID, creatorID, id, hash)
}

func getPendingInvite(ctx context.Context, q tokenHashQuerier, serverID, creatorID, id string, hash []byte) (*Invite, error) {
	selfIdentity := identity.CanonicalID(serverID, creatorID)
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at
		FROM invites
		WHERE created_by = $1 AND id = $2 AND token_hash = $3
	`, selfIdentity, id, hash)
	inv, err := scanInvite(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inv.Status() != "pending" {
		return nil, nil
	}
	return &inv, nil
}

// MarkClaimed claims an unused, unrevoked invite inside tx.
// createdBy + inviteID form the composite primary key; claimedBy is a bare
// userID. Returns whether a row was updated.
func (s *Store) MarkClaimed(
	ctx context.Context,
	tx *sql.Tx,
	createdBy, inviteID, claimedBy string,
	claimedAt time.Time,
) (bool, error) {
	createdByIdentity := identity.CanonicalID(s.ServerID, createdBy)
	claimedByIdentity := identity.CanonicalID(s.ServerID, claimedBy)
	res, err := tx.ExecContext(ctx, `
		UPDATE invites
		SET claimed_at = $3, claimed_by = $4
		WHERE created_by = $1 AND id = $2
		  AND claimed_at IS NULL AND revoked_at IS NULL
	`, createdByIdentity, inviteID, claimedAt.UTC(), claimedByIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Revoke marks an unused invite revoked. Issuer-only (composite key).
func (s *Store) Revoke(
	ctx context.Context,
	inviteID, creatorID string,
	revokedAt time.Time,
) error {
	var claimedAt, existingRevoked sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT claimed_at, revoked_at
		FROM invites WHERE created_by = $1 AND id = $2
	`, creatorID, inviteID).Scan(&claimedAt, &existingRevoked)
	if err == sql.ErrNoRows {
		return ErrInviteNotFound
	}
	if err != nil {
		return err
	}
	if claimedAt.Valid {
		return ErrInviteAlreadyClaimed
	}
	if existingRevoked.Valid {
		return ErrInviteAlreadyRevoked
	}

	res, err := s.DB.ExecContext(ctx, `
		UPDATE invites
		SET revoked_at = $3
		WHERE created_by = $1 AND id = $2 AND claimed_at IS NULL AND revoked_at IS NULL
	`, creatorID, inviteID, revokedAt.UTC())
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
// actual stored form) and keeps that form on Invite's wire-facing fields,
// no decode to bare.
func scanInvite(row scannable) (Invite, error) {
	var inv Invite
	var createdBy identity.IdentityID
	var claimedAt, revokedAt sql.NullTime
	var claimedBy sql.NullString
	err := row.Scan(
		&inv.ID,
		&createdBy,
		&inv.CreatedAt,
		&inv.GrantedRole,
		&claimedAt,
		&claimedBy,
		&revokedAt,
	)
	if err != nil {
		return Invite{}, err
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
	return inv, nil
}
