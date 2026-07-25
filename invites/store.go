package invites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// Invite is the durable invite row (never includes the raw token).
type Invite struct {
	ID        string
	CreatedBy string
	CreatedAt time.Time
	ClaimedAt *time.Time
	ClaimedBy *string
	RevokedAt *time.Time
}

// Store persists invites. MarkClaimed accepts an existing *sql.Tx for signup.
type Store struct {
	DB *sql.DB
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
) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO invites (created_by, id, token_hash, created_at)
		VALUES ($1, $2, $3, $4)
	`, creatorID, id, tokenHash, createdAt.UTC())
	if isUniqueViolation(err) {
		return ErrInviteExists
	}
	return err
}

func (s *Store) GetByCreatorAndID(ctx context.Context, creatorID, id string) (*Invite, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, claimed_at, claimed_by, revoked_at
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

// GetStatusByCreatorAndID returns the invite plus claimed username when set.
func (s *Store) GetStatusByCreatorAndID(
	ctx context.Context,
	creatorID, id string,
) (*Invite, string, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT i.id, i.created_by, i.created_at, i.claimed_at, i.claimed_by, i.revoked_at,
		       COALESCE(u.username, '')
		FROM invites i
		LEFT JOIN users u ON u.id = i.claimed_by
		WHERE i.created_by = $1 AND i.id = $2
	`, creatorID, id)

	var inv Invite
	var claimedAt, revokedAt sql.NullTime
	var claimedBy sql.NullString
	var username string
	err := row.Scan(
		&inv.ID, &inv.CreatedBy, &inv.CreatedAt,
		&claimedAt, &claimedBy, &revokedAt, &username,
	)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
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
	return &inv, username, nil
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

func getByTokenHash(ctx context.Context, q tokenHashQuerier, hash []byte) (*Invite, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, claimed_at, claimed_by, revoked_at
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

// MarkClaimed claims an unused, unrevoked invite inside tx.
// createdBy + inviteID form the composite primary key.
// Returns whether a row was updated.
func (s *Store) MarkClaimed(
	ctx context.Context,
	tx *sql.Tx,
	createdBy, inviteID, claimedBy string,
	claimedAt time.Time,
) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE invites
		SET claimed_at = $3, claimed_by = $4
		WHERE created_by = $1 AND id = $2
		  AND claimed_at IS NULL AND revoked_at IS NULL
	`, createdBy, inviteID, claimedAt.UTC(), claimedBy)
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

func scanInvite(row scannable) (Invite, error) {
	var inv Invite
	var claimedAt, revokedAt sql.NullTime
	var claimedBy sql.NullString
	err := row.Scan(
		&inv.ID,
		&inv.CreatedBy,
		&inv.CreatedAt,
		&claimedAt,
		&claimedBy,
		&revokedAt,
	)
	if err != nil {
		return Invite{}, err
	}
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
