package recovery

import (
	"context"
	"database/sql"

	"syrinx/identity"
)

// unclaimed_accounts.user_id and ongoing_recoveries.user_id both FK to
// identities(id). userID here is always bare, local to serverID, and
// converted internally before touching the query.

// InsertUnclaimed records a peer-seeded account awaiting owner claim.
func InsertUnclaimed(ctx context.Context, db *sql.DB, serverID, userID string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO unclaimed_accounts (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, identity.LocalID(userID, serverID))
	return err
}

// DeleteUnclaimed removes a user from the unclaimed gauge (e.g. after own claim).
func DeleteUnclaimed(ctx context.Context, db *sql.DB, serverID, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM unclaimed_accounts WHERE user_id = $1`, identity.LocalID(userID, serverID))
	return err
}

// InsertOngoing marks a claimant as mid-import (import gate).
func InsertOngoing(ctx context.Context, db *sql.DB, serverID, userID string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO ongoing_recoveries (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, identity.LocalID(userID, serverID))
	return err
}

// DeleteOngoing clears the import gate for a user (e.g. after /complete).
func DeleteOngoing(ctx context.Context, db *sql.DB, serverID, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM ongoing_recoveries WHERE user_id = $1`, identity.LocalID(userID, serverID))
	return err
}

// CountUnclaimed returns how many peer-seeded accounts still await claim.
// No user filter needed — this is a bare row count.
func CountUnclaimed(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unclaimed_accounts`).Scan(&n)
	return n, err
}
