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

// PendingReedRequest represents a pending reed request with its associated event
type PendingReedRequest struct {
	PendingEvent
	ReedID string
}

// CreatePendingEvent inserts a new pending event and its associated reed request in a transaction.
func (ds *DBService) CreatePendingEvent(eventID, requestID, userID string, eventName EventName, reedID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, userID, eventName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO pending_reed_requests (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreateProfileSubscriptionEvent inserts a pending event tied to a profile subscription.
func (ds *DBService) CreateProfileSubscriptionEvent(eventID, requestID, userID string, eventName EventName, reedID, subscriptionID string) error {
	tx, err := ds.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name, subscription_id)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, requestID, userID, eventName, subscriptionID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO pending_reed_requests (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetPendingReedRequest retrieves a pending event and its associated reed ID by event ID
func (ds *DBService) GetPendingReedRequest(eventID string) (*PendingReedRequest, error) {
	var pe PendingReedRequest
	err := ds.db.QueryRow(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, prr.reed_id
		FROM pending_events pe
		JOIN pending_reed_requests prr ON prr.event_id = pe.event_id
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &pe.RequesterUserID, &pe.EventName, &pe.ReedID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &pe, nil
}

// DeletePendingEvent deletes a pending event by event ID (cascades to pending_reed_requests)
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

// AllocateReed records that a user now holds a reed.
func (ds *DBService) AllocateReed(reedID, userID string) error {
	_, err := ds.db.Exec(`
		INSERT INTO reed_allocations (reed_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, reedID, userID)
	return err
}

// DeleteReedAllocation removes a single holder's allocation for a reed.
func (ds *DBService) DeleteReedAllocation(reedID, userID string) error {
	_, err := ds.db.Exec(`
		DELETE FROM reed_allocations WHERE reed_id = $1 AND user_id = $2
	`, reedID, userID)
	return err
}

// GetNextPendingForHolder returns the oldest undispatched pending event for reeds held by holderUserID.
func (ds *DBService) GetNextPendingForHolder(holderUserID string) (*PendingReedRequest, error) {
	var pe PendingReedRequest
	err := ds.db.QueryRow(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, prr.reed_id
		FROM pending_reed_requests prr
		JOIN pending_events pe ON pe.event_id = prr.event_id
		JOIN reed_allocations ra ON ra.reed_id = prr.reed_id
		WHERE ra.user_id = $1
		  AND pe.dispatched_at IS NULL
		ORDER BY pe.created_at
		LIMIT 1
	`, holderUserID).Scan(&pe.EventID, &pe.RequestID, &pe.RequesterUserID, &pe.EventName, &pe.ReedID)
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
func (ds *DBService) GetOnlineReedHolder(reedID string) (string, error) {
	var userID string
	err := ds.db.QueryRow(`
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.user_id = ou.user_id
		WHERE ra.reed_id = $1
		LIMIT 1
	`, reedID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return userID, err
}

// GetPendingEventsForUser returns all pending reed requests for reeds held by the given user
func (ds *DBService) GetPendingEventsForUser(userID string) ([]PendingReedRequest, error) {
	rows, err := ds.db.Query(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, prr.reed_id
		FROM pending_reed_requests prr
		JOIN pending_events pe ON pe.event_id = prr.event_id
		JOIN reed_allocations ra ON ra.reed_id = prr.reed_id
		WHERE ra.user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedRequest
	for rows.Next() {
		var prr PendingReedRequest
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&prr.RequesterUserID,
			&prr.EventName,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		results = append(results, prr)
	}
	return results, nil
}

// GetPendingRequestsForRequester returns all pending reed requests initiated by the given user.
func (ds *DBService) GetPendingRequestsForRequester(requesterUserID string) ([]PendingReedRequest, error) {
	rows, err := ds.db.Query(`
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, prr.reed_id
		FROM pending_reed_requests prr
		JOIN pending_events pe ON pe.event_id = prr.event_id
		WHERE pe.requester_user_id = $1
	`, requesterUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedRequest
	for rows.Next() {
		var prr PendingReedRequest
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&prr.RequesterUserID,
			&prr.EventName,
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
		      WHERE ra.reed_id = r.id AND ra.user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
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
		      WHERE ra.reed_id = r.id AND ra.user_id = $1
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
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
		      WHERE ra.reed_id = r.id AND ra.user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
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
			SELECT user_id
			FROM broadcast_subscriptions
			WHERE user_id != $1
			  AND (last_delivery IS NULL OR last_delivery < NOW() - INTERVAL '1 second')
			ORDER BY last_delivery ASC NULLS FIRST
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

// ReedExists reports whether a reed with the given ID exists in the reeds table.
func (ds *DBService) ReedExists(reedID string) (bool, error) {
	var exists bool
	err := ds.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM reeds WHERE id = $1)`, reedID).Scan(&exists)
	return exists, err
}

// MissingRemoval is a reed_allocations ∩ reed_removals row for catch-up.
type MissingRemoval struct {
	ReedID string
	Cert   map[string]interface{}
}

// GetMissingRemovals returns removal certs for reeds this user still holds.
func (ds *DBService) GetMissingRemovals(userID string) ([]MissingRemoval, error) {
	rows, err := ds.db.Query(`
		SELECT rr.reed_id, rr.user_id, rr.user_signature,
		       rr.server_signature, rr.server_fingerprint, rr.server_signed_at
		FROM reed_allocations ra
		JOIN reed_removals rr ON rr.reed_id = ra.reed_id
		WHERE ra.user_id = $1
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
		var reedID, authorID, userSig, serverSig, serverFP string
		var signedAt time.Time
		if err := rows.Scan(&reedID, &authorID, &userSig, &serverSig, &serverFP, &signedAt); err != nil {
			return nil, err
		}
		out = append(out, MissingRemoval{
			ReedID: reedID,
			Cert:   reedRemovalWireMap(serverID, authorID, reedID, userSig, serverSig, serverFP, signedAt),
		})
	}
	return out, nil
}

// GetReedRemovalWire loads a removal cert as a wire-shaped map for WS delivery.
func (ds *DBService) GetReedRemovalWire(reedID string) (map[string]interface{}, error) {
	cert, err := deletion.GetCertByReedID(ds.db, reedID)
	if err != nil || cert == nil {
		return nil, err
	}
	serverID, err := ds.serverID()
	if err != nil {
		return nil, err
	}
	return reedRemovalWireMap(
		serverID, cert.UserID, cert.ReedID,
		cert.UserSignature, cert.ServerSignature, cert.ServerFingerprint, cert.ServerSignedAt,
	), nil
}

func (ds *DBService) serverID() (string, error) {
	var id string
	err := ds.db.QueryRow(`SELECT id FROM servers WHERE self = TRUE`).Scan(&id)
	return id, err
}

func reedRemovalWireMap(
	serverID, userID, reedID, userSig, serverSig, serverFP string, signedAt time.Time,
) map[string]interface{} {
	return map[string]interface{}{
		"type":      identity.TypeReed,
		"serverID":  serverID,
		"userID":    userID,
		"reedID":    reedID,
		"signature": userSig,
		"server": map[string]interface{}{
			"id":          serverID,
			"fingerprint": serverFP,
			"algorithm":   identity.Algorithm,
			"signature":   serverSig,
			"timestamp":   signedAt.UTC(),
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
		SELECT ar.user_id, ar.note, ar.user_signature, ar.server_signature,
		       ar.server_fingerprint, ar.server_signed_at
		FROM account_removals ar
		WHERE EXISTS (
			SELECT 1 FROM user_following uf
			WHERE uf.user_id = $1 AND uf.following_user_id = ar.user_id
		) OR EXISTS (
			SELECT 1 FROM reed_allocations ra
			JOIN reeds r ON r.id = ra.reed_id
			WHERE ra.user_id = $1 AND r.user_id = ar.user_id
		)
	`, viewerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MissingAccountRemoval
	for rows.Next() {
		var removedUserID, note, userSig, serverSig, serverFP string
		var signedAt time.Time
		if err := rows.Scan(&removedUserID, &note, &userSig, &serverSig, &serverFP, &signedAt); err != nil {
			return nil, err
		}
		out = append(out, MissingAccountRemoval{
			UserID: removedUserID,
			Cert:   accountRemovalWireMap(serverID, removedUserID, note, userSig, serverSig, serverFP, signedAt),
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
	return accountRemovalWireMap(
		serverID, cert.UserID, cert.Note,
		cert.UserSignature, cert.ServerSignature, cert.ServerFingerprint, cert.ServerSignedAt,
	), nil
}

func accountRemovalWireMap(
	serverID, userID, note, userSig, serverSig, serverFP string, signedAt time.Time,
) map[string]interface{} {
	return map[string]interface{}{
		"type":      identity.TypeAccount,
		"serverID":  serverID,
		"userID":    userID,
		"note":      note,
		"signature": userSig,
		"server": map[string]interface{}{
			"id":          serverID,
			"fingerprint": serverFP,
			"algorithm":   identity.Algorithm,
			"signature":   serverSig,
			"timestamp":   signedAt.UTC(),
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
		WHERE user_id = $1
		  AND reed_id IN (SELECT id FROM reeds WHERE user_id = $2)
	`, viewerUserID, removedUserID); err != nil {
		return err
	}
	return tx.Commit()
}
