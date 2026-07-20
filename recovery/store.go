package recovery

import (
	"database/sql"
)

// InsertUnclaimed records a peer-seeded account awaiting owner claim.
func InsertUnclaimed(db *sql.DB, userID string) error {
	_, err := db.Exec(`
		INSERT INTO unclaimed_accounts (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, userID)
	return err
}

// DeleteUnclaimed removes a user from the unclaimed gauge (e.g. after own claim).
func DeleteUnclaimed(db *sql.DB, userID string) error {
	_, err := db.Exec(`DELETE FROM unclaimed_accounts WHERE user_id = $1`, userID)
	return err
}

// InsertOngoing marks a claimant as mid-import (import gate).
func InsertOngoing(db *sql.DB, userID string) error {
	_, err := db.Exec(`
		INSERT INTO ongoing_recoveries (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, userID)
	return err
}

// DeleteOngoing clears the import gate for a user (e.g. after /complete).
func DeleteOngoing(db *sql.DB, userID string) error {
	_, err := db.Exec(`DELETE FROM ongoing_recoveries WHERE user_id = $1`, userID)
	return err
}

// CountUnclaimed returns how many peer-seeded accounts still await claim.
func CountUnclaimed(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM unclaimed_accounts`).Scan(&n)
	return n, err
}
