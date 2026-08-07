package coverage

import (
	"context"
	"database/sql"
)

// Percent returns floor(100 * holders / activeUsers), capped at 100.
func Percent(holders, activeUsers int) int {
	if activeUsers <= 0 {
		return 0
	}
	p := (100 * holders) / activeUsers
	if p > 100 {
		return 100
	}
	return p
}

// BumpActiveUsers adjusts the singleton active-user counter in the same TX.
func BumpActiveUsers(ctx context.Context, tx *sql.Tx, delta int) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE network_stats
		SET active_users = GREATEST(0, active_users + $1)
		WHERE id = TRUE
	`, delta)
	return err
}

// BumpAllocationCount adjusts per-reed holder count in the same TX.
func BumpAllocationCount(ctx context.Context, tx *sql.Tx, authorUserID, reedID string, delta int) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE reeds
		SET allocation_count = GREATEST(0, allocation_count + $1)
		WHERE user_id = $2 AND id = $3
	`, delta, authorUserID, reedID)
	return err
}

// ActiveUsers reads the network-wide active user count.
func ActiveUsers(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT active_users FROM network_stats WHERE id = TRUE`).Scan(&n)
	return n, err
}

// ActiveUsersTx reads active users inside an open transaction.
func ActiveUsersTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT active_users FROM network_stats WHERE id = TRUE`).Scan(&n)
	return n, err
}
