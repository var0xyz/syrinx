package realtime

import (
	"database/sql"
	"time"

	"github.com/rs/zerolog/log"
)

// DBService handles database operations for the realtime service
type DBService struct {
	db *sql.DB
}

// NewDBService creates a new database service
func NewDBService(db *sql.DB) *DBService {
	return &DBService{db: db}
}

// MarkUserOnline marks a user as online in the database
func (ds *DBService) MarkUserOnline(userID string) error {
	_, err := ds.db.Exec(`
		INSERT INTO online_users (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, userID)

	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to mark user as online")
		return err
	}

	log.Debug().
		Str("userID", userID).
		Msg("User came online")

	return nil
}

// MarkUserOffline marks a user as offline in the database
func (ds *DBService) MarkUserOffline(userID string) error {
	_, err := ds.db.Exec(`
		DELETE FROM online_users WHERE user_id = $1
	`, userID)

	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to mark user as offline")
		return err
	}

	log.Debug().
		Str("userID", userID).
		Msg("User marked as offline")

	return nil
}

// GetOnlineUsers returns a list of currently online user IDs
func (ds *DBService) GetOnlineUsers() ([]string, error) {
	rows, err := ds.db.Query(`
		SELECT user_id FROM online_users
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		users = append(users, userID)
	}

	return users, nil
}

// CleanupStaleConnections removes connections older than the specified duration
func (ds *DBService) CleanupStaleConnections(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge)

	result, err := ds.db.Exec(`
		DELETE FROM online_users WHERE created_at < $1
	`, cutoff)

	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to cleanup stale connections")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		log.Info().
			Int64("count", rowsAffected).
			Dur("maxAge", maxAge).
			Msg("Cleaned up stale connections")
	}

	return nil
}

// GetUserPublicKey retrieves a user's public key by fingerprint
func (ds *DBService) GetUserPublicKey(userID, fingerprint string) (string, error) {
	var armor string
	err := ds.db.QueryRow(`
		SELECT armor
		FROM user_keys
		WHERE user_id = $1 AND fingerprint = $2
	`, userID, fingerprint).Scan(&armor)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	return armor, nil
}

// GetUserByID retrieves basic user information
func (ds *DBService) GetUserByID(userID string) (*User, error) {
	var user User
	var avatarURL sql.NullString
	var bio sql.NullString

	err := ds.db.QueryRow(`
		SELECT id, username, avatar_url, bio, created_at
		FROM users
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&user.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if avatarURL.Valid {
		user.AvatarURL = avatarURL.String
	}
	if bio.Valid {
		user.Bio = bio.String
	}

	return &user, nil
}

// User represents a user in the system
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatarURL"`
	Bio       string    `json:"bio"`
	CreatedAt time.Time `json:"memberSince"`
}

// SubscribeToBroadcast adds a user to the broadcast subscriptions table
func (ds *DBService) SubscribeToBroadcast(userID string) error {
	_, err := ds.db.Exec(`
		INSERT INTO broadcast_subscriptions (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, userID)

	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to subscribe user to broadcast")
		return err
	}

	return nil
}

// UnsubscribeFromBroadcast removes a user from the broadcast subscriptions table
func (ds *DBService) UnsubscribeFromBroadcast(userID string) error {
	_, err := ds.db.Exec(`
		DELETE FROM broadcast_subscriptions WHERE user_id = $1
	`, userID)

	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to unsubscribe user from broadcast")
		return err
	}

	return nil
}

// GetBroadcastSubscribers returns a list of all user IDs subscribed to broadcast
func (ds *DBService) GetBroadcastSubscribers() ([]string, error) {
	rows, err := ds.db.Query(`
		SELECT DISTINCT user_id FROM broadcast_subscriptions
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, userID)
	}

	return subscribers, nil
}

// GetOnlineFollowers returns the IDs of online users who follow the given author
func (ds *DBService) GetOnlineFollowers(authorID string) ([]string, error) {
	rows, err := ds.db.Query(`
		SELECT ou.user_id
		FROM online_users ou
		JOIN user_followers uf ON ou.user_id = uf.follower_user_id
		WHERE uf.user_id = $1
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followers []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		followers = append(followers, userID)
	}

	return followers, nil
}

// GetBroadcastSubscribersNotFollowing returns broadcast subscribers who do not follow the given author,
// ensuring they are complementary to the online followers group and receive no duplicate notifications
func (ds *DBService) GetBroadcastSubscribersNotFollowing(authorID string) ([]string, error) {
	rows, err := ds.db.Query(`
		SELECT bs.user_id
		FROM broadcast_subscriptions bs
		WHERE NOT EXISTS (
			SELECT 1 FROM user_followers uf
			WHERE uf.follower_user_id = bs.user_id
			AND uf.user_id = $1
		)
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, userID)
	}

	return subscribers, nil
}
