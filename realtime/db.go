package realtime

import (
	"database/sql"
	"time"

	"syrinx/deletion"
	"syrinx/identity"

	"github.com/lib/pq"
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

// SetSyncRequestID stores the client-provided sync request ID for a user.
func (ds *DBService) SetSyncRequestID(userID, requestID string) error {
	_, err := ds.db.Exec(`
		UPDATE online_users SET sync_request_id = $1 WHERE user_id = $2
	`, requestID, userID)
	return err
}

// GetSyncRequestID returns the stored sync request ID for a user, or "" if not set.
func (ds *DBService) GetSyncRequestID(userID string) (string, error) {
	var id string
	err := ds.db.QueryRow(`
		SELECT COALESCE(sync_request_id, '') FROM online_users WHERE user_id = $1
	`, userID).Scan(&id)
	return id, err
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

// GetUserPublicKey retrieves a user's public key by fingerprint
func (ds *DBService) GetUserPublicKey(userID, fingerprint string) (string, error) {
	var armor string
	err := ds.db.QueryRow(`
		SELECT armor
		FROM user_keys
		WHERE owner = $1 AND fingerprint = $2
	`, userID, fingerprint).Scan(&armor)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	return armor, nil
}

// GetUsername returns the current username for display on ephemeral deliveries
// (e.g. broadcast reeds). Empty string when the user row is missing.
func (ds *DBService) GetUsername(userID string) (string, error) {
	var username string
	err := ds.db.QueryRow(`SELECT username FROM users WHERE id = $1`, userID).Scan(&username)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return username, err
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

// PendingEvent represents a pending relay event stored in the database
type PendingEvent struct {
	EventID         string
	RequestID       string
	RequesterUserID string
	EventName       string
}

// PendingReedEvent is a pending_events row with its pending_reed_events subject.
type PendingReedEvent struct {
	PendingEvent
	UserID string // author
	ReedID string
}

// PendingAccountEvent is a pending_events row with its pending_account_events subject.
type PendingAccountEvent struct {
	PendingEvent
	UserID string // removed account
}

// PendingSubject is the ACK/lookup view: reed and/or account fields depending on event_name.
type PendingSubject struct {
	PendingEvent
	UserID string // author (reed) or removed account
	ReedID string // set for reed events only
}

// CreatePendingReedEvent inserts pending_events + pending_reed_events (FK to reeds).
// requesterUserID is the viewer; authorUserID + reedID identify the reed subject.
func (ds *DBService) CreatePendingReedEvent(eventID, requestID, requesterUserID string, eventName EventName, authorUserID, reedID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requesterUserID, eventName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO pending_reed_events (event_id, user_id, reed_id)
		VALUES ($1, $2, $3)
	`, eventID, authorUserID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreatePendingAccountEvent inserts pending_events + pending_account_events.
func (ds *DBService) CreatePendingAccountEvent(eventID, requestID, requesterUserID, removedUserID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requesterUserID, AccountRemovedEvent)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO pending_account_events (event_id, user_id)
		VALUES ($1, $2)
	`, eventID, removedUserID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreateProfileSubscriptionEvent inserts a reed pending event tied to a profile subscription.
func (ds *DBService) CreateProfileSubscriptionEvent(eventID, requestID, requesterUserID string, eventName EventName, authorUserID, reedID, subscriptionID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name, subscription_id)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, requestID, requesterUserID, eventName, subscriptionID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO pending_reed_events (event_id, user_id, reed_id)
		VALUES ($1, $2, $3)
	`, eventID, authorUserID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetPendingSubject loads a pending event and its typed child subject by event ID.
func (ds *DBService) GetPendingSubject(eventID string) (*PendingSubject, error) {
	var pe PendingSubject
	err := ds.db.QueryRow(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name
		FROM pending_events pe
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &pe.RequesterUserID, &pe.EventName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if EventName(pe.EventName) == AccountRemovedEvent {
		err = ds.db.QueryRow(`
			SELECT user_id FROM pending_account_events WHERE event_id = $1
		`, eventID).Scan(&pe.UserID)
	} else {
		err = ds.db.QueryRow(`
			SELECT user_id, reed_id FROM pending_reed_events WHERE event_id = $1
		`, eventID).Scan(&pe.UserID, &pe.ReedID)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &pe, nil
}

// GetPendingReedEvent loads a reed-subject pending event (nil if missing or account event).
func (ds *DBService) GetPendingReedEvent(eventID string) (*PendingReedEvent, error) {
	var pe PendingReedEvent
	err := ds.db.QueryRow(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &pe.RequesterUserID, &pe.EventName, &pe.UserID, &pe.ReedID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &pe, nil
}

// DeletePendingEvent deletes a pending event by event ID (cascades to child subject tables).
func (ds *DBService) DeletePendingEvent(eventID string) error {
	_, err := ds.db.Exec(`DELETE FROM pending_events WHERE event_id = $1`, eventID)
	return err
}

// DeletePendingEventsByUser deletes all pending events for a given requester user ID
func (ds *DBService) DeletePendingEventsByUser(userID string) error {
	_, err := ds.db.Exec(`DELETE FROM pending_events WHERE requester_user_id = $1`, userID)
	return err
}

// DeleteProfileSubscriptionsByViewer deletes all profile subscriptions for a given viewer.
func (ds *DBService) DeleteProfileSubscriptionsByViewer(userID string) error {
	_, err := ds.db.Exec(`DELETE FROM profile_subscriptions WHERE viewer_user_id = $1`, userID)
	return err
}

// AllocateReed records that holderUserID now holds the reed authored by authorUserID.
func (ds *DBService) AllocateReed(reedID, holderUserID, authorUserID string) error {
	_, err := ds.db.Exec(`
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, reedID, holderUserID, authorUserID)
	return err
}

// DeleteReedAllocation removes a single holder's allocation for a reed.
func (ds *DBService) DeleteReedAllocation(authorUserID, reedID, holderUserID string) error {
	_, err := ds.db.Exec(`
		DELETE FROM reed_allocations
		WHERE author_user_id = $1 AND reed_id = $2 AND holder_user_id = $3
	`, authorUserID, reedID, holderUserID)
	return err
}

// GetNextPendingForHolder returns the oldest undispatched reed pending for reeds held by holderUserID.
func (ds *DBService) GetNextPendingForHolder(holderUserID string) (*PendingReedEvent, error) {
	var pe PendingReedEvent
	err := ds.db.QueryRow(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra
		  ON ra.reed_id = pre.reed_id AND ra.author_user_id = pre.user_id
		WHERE ra.holder_user_id = $1
		  AND pe.dispatched_at IS NULL
		ORDER BY pe.created_at
		LIMIT 1
	`, holderUserID).Scan(&pe.EventID, &pe.RequestID, &pe.RequesterUserID, &pe.EventName, &pe.UserID, &pe.ReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pe, nil
}

// MarkEventDispatched marks an event as dispatched. Returns true if the update claimed the row
// (i.e. it was still undispatched), false if another replica already claimed it.
func (ds *DBService) MarkEventDispatched(eventID string) (bool, error) {
	result, err := ds.db.Exec(`
		UPDATE pending_events SET dispatched_at = NOW()
		WHERE event_id = $1 AND dispatched_at IS NULL
	`, eventID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ResetDispatchedAt clears dispatched_at for an event, making it eligible for dispatch again.
func (ds *DBService) ResetDispatchedAt(eventID string) error {
	_, err := ds.db.Exec(`
		UPDATE pending_events SET dispatched_at = NULL WHERE event_id = $1
	`, eventID)
	return err
}

// GetOnlineReedHolder returns the user ID of one online holder of the given reed,
// or an empty string if no holder is currently online.
func (ds *DBService) GetOnlineReedHolder(authorUserID, reedID string) (string, error) {
	var userID string
	err := ds.db.QueryRow(`
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.holder_user_id = ou.user_id
		WHERE ra.author_user_id = $1 AND ra.reed_id = $2
		LIMIT 1
	`, authorUserID, reedID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return userID, err
}

// GetOnlineReedHolderExcluding returns one online holder other than excludeUserID.
func (ds *DBService) GetOnlineReedHolderExcluding(authorUserID, reedID, excludeUserID string) (string, error) {
	var userID string
	err := ds.db.QueryRow(`
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.holder_user_id = ou.user_id
		WHERE ra.author_user_id = $1 AND ra.reed_id = $2 AND ou.user_id != $3
		LIMIT 1
	`, authorUserID, reedID, excludeUserID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return userID, err
}

// ClaimPendingFanout removes the pending_fanout row if present. Returns true when
// this call claimed fanout (row deleted). Concurrent READY messages only claim once.
func (ds *DBService) ClaimPendingFanout(authorUserID, reedID string) (bool, error) {
	var id string
	err := ds.db.QueryRow(`
		DELETE FROM pending_fanout
		WHERE user_id = $1 AND reed_id = $2
		RETURNING reed_id
	`, authorUserID, reedID).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetPendingEventsForUser returns all pending reed events for reeds held by the given user.
func (ds *DBService) GetPendingEventsForUser(userID string) ([]PendingReedEvent, error) {
	rows, err := ds.db.Query(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra
		  ON ra.reed_id = pre.reed_id AND ra.author_user_id = pre.user_id
		WHERE ra.holder_user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedEvent
	for rows.Next() {
		var prr PendingReedEvent
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&prr.RequesterUserID,
			&prr.EventName,
			&prr.UserID,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		results = append(results, prr)
	}
	return results, nil
}

// GetPendingRequestsForRequester returns pending reed events initiated by the given user
// (reed relay retry only — not account events).
func (ds *DBService) GetPendingRequestsForRequester(requesterUserID string) ([]PendingReedEvent, error) {
	rows, err := ds.db.Query(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		WHERE pe.requester_user_id = $1
	`, requesterUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedEvent
	for rows.Next() {
		var prr PendingReedEvent
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&prr.RequesterUserID,
			&prr.EventName,
			&prr.UserID,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		results = append(results, prr)
	}
	return results, nil
}

// GetMissingReedIDsForViewer returns IDs of reeds by authorID that viewerID does not yet have,
// excluding any IDs the viewer already holds locally (ownedIDs) or via reed_allocations.
func (ds *DBService) GetMissingReedIDsForViewer(authorID, viewerID string, ownedIDs []string) ([]string, error) {
	if ownedIDs == nil {
		ownedIDs = []string{}
	}
	rows, err := ds.db.Query(`
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND r.id <> ALL($3)
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.author_user_id = r.user_id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
		  )
	`, authorID, viewerID, pq.Array(ownedIDs))
	if err != nil {
		return nil, err
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
	return ids, nil
}

// UnallocatedReed holds a reed ID and its author, used when computing delivery diffs.
type UnallocatedReed struct {
	ReedID   string
	AuthorID string
}

// GetMissingOut returns all reeds from authors that userID follows
// which are not yet present in reed_allocations for that user.
func (ds *DBService) GetMissingOut(userID string) ([]UnallocatedReed, error) {
	rows, err := ds.db.Query(`
		SELECT r.id, r.user_id
		FROM reeds r
		JOIN user_following uf ON uf.following_user_id = r.user_id
		WHERE uf.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.author_user_id = r.user_id AND ra.holder_user_id = $1
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
		  )
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UnallocatedReed
	for rows.Next() {
		var reed UnallocatedReed
		if err := rows.Scan(&reed.ReedID, &reed.AuthorID); err != nil {
			return nil, err
		}
		results = append(results, reed)
	}
	return results, nil
}

// GetUnallocatedReeds returns IDs of reeds by authorID that viewerID does not have in reed_allocations.
func (ds *DBService) GetUnallocatedReeds(authorID, viewerID string) ([]string, error) {
	rows, err := ds.db.Query(`
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.author_user_id = r.user_id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
		  )
	`, authorID, viewerID)
	if err != nil {
		return nil, err
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
	return ids, nil
}

// CreateProfileSubscription records an active profile feed subscription for a viewer.
func (ds *DBService) CreateProfileSubscription(subscriptionID, viewerUserID, authorUserID string) error {
	_, err := ds.db.Exec(`
		INSERT INTO profile_subscriptions (subscription_id, viewer_user_id, author_user_id)
		VALUES ($1, $2, $3)
	`, subscriptionID, viewerUserID, authorUserID)
	return err
}

// GetProfileSubscription returns the subscription ID for an active (viewer, author) pair.
// Returns an empty string when no subscription exists.
func (ds *DBService) GetProfileSubscription(viewerUserID, authorUserID string) (string, error) {
	var id string
	err := ds.db.QueryRow(`
		SELECT subscription_id FROM profile_subscriptions
		WHERE viewer_user_id = $1 AND author_user_id = $2
	`, viewerUserID, authorUserID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// DeleteProfileSubscription deletes a subscription by ID, cascading to its pending_events.
func (ds *DBService) DeleteProfileSubscription(subscriptionID string) error {
	_, err := ds.db.Exec(`
		DELETE FROM profile_subscriptions WHERE subscription_id = $1
	`, subscriptionID)
	return err
}

// ProfileSubscriber represents an active profile feed subscription.
type ProfileSubscriber struct {
	SubscriptionID string
	ViewerUserID   string
}

// GetProfileSubscribers returns all active profile subscriptions for the given author.
func (ds *DBService) GetProfileSubscribers(authorID string) ([]ProfileSubscriber, error) {
	rows, err := ds.db.Query(`
		SELECT subscription_id, viewer_user_id
		FROM profile_subscriptions
		WHERE author_user_id = $1
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []ProfileSubscriber
	for rows.Next() {
		var subscriber ProfileSubscriber
		if err := rows.Scan(&subscriber.SubscriptionID, &subscriber.ViewerUserID); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, subscriber)
	}
	return subscribers, nil
}

// GetBroadcastSubscribers returns up to 100 broadcast subscribers for the given author,
// throttled to one delivery per second per subscriber.
// Followers of the author are excluded — they receive the reed via the follow
// path (new_reed → followcast), not as ephemeral broadcast.
// Subscribers are selected in order of oldest last_delivery (NULLS FIRST) and their
// last_delivery timestamp is updated atomically.
//
// NOTE: This UPDATE is not serialised across replicas. Two concurrent replicas could select
// the same batch before either writes last_delivery, causing up to 100 users to receive a
// duplicate. At our current replica count this is acceptable — duplicates are harmless (the
// client deduplicates by reed ID) and the race window is tiny.
func (ds *DBService) GetBroadcastSubscribers(authorID string) ([]string, error) {
	rows, err := ds.db.Query(`
		WITH eligible AS (
			SELECT bs.user_id
			FROM broadcast_subscriptions bs
			WHERE bs.user_id != $1
			  AND (bs.last_delivery IS NULL OR bs.last_delivery < NOW() - INTERVAL '1 second')
			  AND NOT EXISTS (
				SELECT 1 FROM user_following uf
				WHERE uf.user_id = bs.user_id AND uf.following_user_id = $1
			  )
			ORDER BY bs.last_delivery ASC NULLS FIRST
			LIMIT 100
		),
		updated AS (
			UPDATE broadcast_subscriptions
			SET last_delivery = NOW()
			WHERE user_id IN (SELECT user_id FROM eligible)
		)
		SELECT user_id FROM eligible
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

// ReedExists reports whether (authorID, reedID) exists in the reeds table.
func (ds *DBService) ReedExists(authorID, reedID string) (bool, error) {
	var exists bool
	err := ds.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM reeds WHERE user_id = $1 AND id = $2)
	`, authorID, reedID).Scan(&exists)
	return exists, err
}

// MissingRemoval is a reed_allocations ∩ reed_removals row for catch-up.
type MissingRemoval struct {
	ReedID string
	UserID string
	Cert   map[string]interface{}
}

// GetMissingRemovals returns removal certs for reeds this user still holds.
func (ds *DBService) GetMissingRemovals(userID string) ([]MissingRemoval, error) {
	rows, err := ds.db.Query(`
		SELECT rr.reed_id, rr.user_id
		FROM reed_allocations ra
		JOIN reed_removals rr
		  ON rr.reed_id = ra.reed_id AND rr.user_id = ra.author_user_id
		WHERE ra.holder_user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	serverID, err := ds.serverID()
	if err != nil {
		return nil, err
	}

	var out []MissingRemoval
	for rows.Next() {
		var reedID, authorID string
		if err := rows.Scan(&reedID, &authorID); err != nil {
			return nil, err
		}
		cert, err := deletion.GetCert(ds.db, authorID, reedID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, MissingRemoval{
			ReedID: reedID,
			UserID: authorID,
			Cert:   reedRemovalWireMap(serverID, cert),
		})
	}
	return out, rows.Err()
}

// GetReedRemovalWire loads a removal cert as a wire-shaped map for WS delivery.
func (ds *DBService) GetReedRemovalWire(authorID, reedID string) (map[string]interface{}, error) {
	cert, err := deletion.GetCert(ds.db, authorID, reedID)
	if err != nil || cert == nil {
		return nil, err
	}
	serverID, err := ds.serverID()
	if err != nil {
		return nil, err
	}
	return reedRemovalWireMap(serverID, cert), nil
}

func (ds *DBService) serverID() (string, error) {
	var id string
	err := ds.db.QueryRow(`SELECT id FROM servers WHERE self = TRUE`).Scan(&id)
	return id, err
}

func reedRemovalWireMap(serverID string, cert *deletion.Cert) map[string]interface{} {
	return map[string]interface{}{
		"type":     identity.TypeReed,
		"serverID": serverID,
		"userID":   cert.UserID,
		"reedID":   cert.ReedID,
		"userSignature": map[string]interface{}{
			"fingerprint": cert.UserFingerprint,
			"armor":       cert.UserSignature,
		},
		"serverSignature": map[string]interface{}{
			"serverID":    serverID,
			"fingerprint": cert.ServerFingerprint,
			"armor":       cert.ServerSignature,
			"timestamp":   cert.ServerSignedAt.UTC(),
		},
	}
}

// MissingAccountRemoval is a catch-up row: viewer still follows or holds
// allocations for a removed author's reeds.
type MissingAccountRemoval struct {
	UserID string
	Cert   map[string]interface{}
}

// GetMissingAccountRemovals returns account_removals that still apply to viewer
// (follow ∪ allocations for that author's reeds).
func (ds *DBService) GetMissingAccountRemovals(viewerUserID string) ([]MissingAccountRemoval, error) {
	serverID, err := ds.serverID()
	if err != nil {
		return nil, err
	}
	rows, err := ds.db.Query(`
		SELECT ar.user_id
		FROM account_removals ar
		WHERE EXISTS (
			SELECT 1 FROM user_following uf
			WHERE uf.user_id = $1 AND uf.following_user_id = ar.user_id
		) OR EXISTS (
			SELECT 1 FROM reed_allocations ra
			WHERE ra.holder_user_id = $1 AND ra.author_user_id = ar.user_id
		)
	`, viewerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MissingAccountRemoval
	for rows.Next() {
		var removedUserID string
		if err := rows.Scan(&removedUserID); err != nil {
			return nil, err
		}
		cert, err := deletion.GetAccountCert(ds.db, removedUserID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, MissingAccountRemoval{
			UserID: removedUserID,
			Cert:   accountRemovalWireMap(serverID, cert),
		})
	}
	return out, rows.Err()
}

// GetAccountRemovalWire loads an account-removal cert as a wire-shaped map.
func (ds *DBService) GetAccountRemovalWire(userID string) (map[string]interface{}, error) {
	cert, err := deletion.GetAccountCert(ds.db, userID)
	if err != nil || cert == nil {
		return nil, err
	}
	serverID, err := ds.serverID()
	if err != nil {
		return nil, err
	}
	return accountRemovalWireMap(serverID, cert), nil
}

func accountRemovalWireMap(serverID string, cert *deletion.AccountCert) map[string]interface{} {
	return map[string]interface{}{
		"type":     identity.TypeAccount,
		"serverID": serverID,
		"userID":   cert.UserID,
		"note":     cert.Note,
		"userSignature": map[string]interface{}{
			"fingerprint": cert.UserFingerprint,
			"armor":       cert.UserSignature,
		},
		"serverSignature": map[string]interface{}{
			"serverID":    serverID,
			"fingerprint": cert.ServerFingerprint,
			"armor":       cert.ServerSignature,
			"timestamp":   cert.ServerSignedAt.UTC(),
		},
	}
}

// ClearPeerStateForRemovedAccount drops follow edges and allocations so
// catch-up no longer re-delivers the account cert to this viewer.
func (ds *DBService) ClearPeerStateForRemovedAccount(viewerUserID, removedUserID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []struct {
		q  string
		a1 string
		a2 string
	}{
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, viewerUserID, removedUserID},
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, removedUserID, viewerUserID},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, removedUserID, viewerUserID},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, viewerUserID, removedUserID},
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s.q, s.a1, s.a2); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		DELETE FROM reed_allocations
		WHERE holder_user_id = $1 AND author_user_id = $2
	`, viewerUserID, removedUserID); err != nil {
		return err
	}
	return tx.Commit()
}
