package invites

import (
	"context"
	"database/sql"
	"time"
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
		INSERT INTO invites (id, token_hash, created_by, created_at)
		VALUES ($1, $2, $3, $4)
	`, id, tokenHash, creatorID, createdAt.UTC())
	return err
}

func (s *Store) ListByCreator(ctx context.Context, creatorID string) ([]Invite, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, created_by, created_at, claimed_at, claimed_by, revoked_at
		FROM invites
		WHERE created_by = $1
		ORDER BY created_at DESC
	`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) GetByTokenHash(ctx context.Context, hash []byte) (*Invite, error) {
	row := s.DB.QueryRowContext(ctx, `
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
// Returns whether a row was updated.
func (s *Store) MarkClaimed(
	ctx context.Context,
	tx *sql.Tx,
	inviteID, claimedBy string,
	claimedAt time.Time,
) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE invites
		SET claimed_at = $2, claimed_by = $3
		WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, claimedAt.UTC(), claimedBy)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Revoke marks an unused invite revoked. Issuer-only.
func (s *Store) Revoke(
	ctx context.Context,
	inviteID, creatorID string,
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
	if createdBy != creatorID {
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
		WHERE id = $1 AND created_by = $3 AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, revokedAt.UTC(), creatorID)
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
