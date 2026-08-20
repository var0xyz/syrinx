package realtime

import (
	"context"
	"database/sql"

	"syrinx/coverage"
	"syrinx/deletion"
	"syrinx/identity"

	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
)

// DBService handles database operations for the realtime service
type DBService struct {
	db       *sql.DB
	serverID string
}

// NewDBService creates a new database service. Every userID/authorID/
// viewerID/etc. string parameter in this file is the full "userID@serverID"
// form already, not bare — do not compose identity.CanonicalID against it.
func NewDBService(db *sql.DB, serverID string) *DBService {
	return &DBService{db: db, serverID: serverID}
}

// MarkUserOnline marks a user as online in the database
func (ds *DBService) MarkUserOnline(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO online_users (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, selfIdentity)

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
func (ds *DBService) SetSyncRequestID(ctx context.Context, userID, requestID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `
		UPDATE online_users SET sync_request_id = $1 WHERE user_id = $2
	`, requestID, selfIdentity)
	return err
}

// GetSyncRequestID returns the stored sync request ID for a user, or "" if not set.
func (ds *DBService) GetSyncRequestID(ctx context.Context, userID string) (string, error) {
	selfIdentity := identity.IdentityID(userID)
	var id string
	err := ds.db.QueryRowContext(ctx, `
		SELECT COALESCE(sync_request_id, '') FROM online_users WHERE user_id = $1
	`, selfIdentity).Scan(&id)
	return id, err
}

// MarkUserOffline marks a user as offline in the database
func (ds *DBService) MarkUserOffline(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `
		DELETE FROM online_users WHERE user_id = $1
	`, selfIdentity)

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

// GetUserPublicKey retrieves a user's public key by fingerprint. user_keys.owner
// is FK'd to identities(id); userID here is always local.
func (ds *DBService) GetUserPublicKey(ctx context.Context, userID, fingerprint string) (string, error) {
	selfIdentity := identity.IdentityID(userID)
	var armor string
	err := ds.db.QueryRowContext(ctx, `
		SELECT armor
		FROM user_keys
		WHERE owner = $1 AND fingerprint = $2
	`, selfIdentity, fingerprint).Scan(&armor)

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
//
// users.id IS identities.id directly, and userID here already arrives in
// that form, so this queries users.id directly, no join needed.
func (ds *DBService) GetUsername(ctx context.Context, userID string) (string, error) {
	var name sql.NullString
	if err := ds.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		return "", err
	}
	if !name.Valid {
		return "", sql.ErrNoRows
	}
	return name.String, nil
}

// SubscribeToBroadcast adds a user to the broadcast subscriptions table.
// broadcast_subscriptions.user_id has no direct FK to identities but is
// composite-FK'd to online_users(user_id), which is itself FK'd to identities(id).
func (ds *DBService) SubscribeToBroadcast(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO broadcast_subscriptions (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, selfIdentity)

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
func (ds *DBService) UnsubscribeFromBroadcast(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `
		DELETE FROM broadcast_subscriptions WHERE user_id = $1
	`, selfIdentity)

	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to unsubscribe user from broadcast")
		return err
	}

	return nil
}


// GetOnlineFollowers returns the IDs of online users who follow the given author.
// online_users.user_id and user_followers.user_id/follower_user_id are all
// direct FKs to identities(id).
func (ds *DBService) GetOnlineFollowers(ctx context.Context, authorID string) ([]string, error) {
	authorIdentity := identity.IdentityID(authorID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT ou.user_id
		FROM online_users ou
		JOIN user_followers uf ON ou.user_id = uf.follower_user_id
		WHERE uf.user_id = $1
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followers []string
	for rows.Next() {
		var userID identity.IdentityID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		followers = append(followers, string(userID))
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
// Every caller here already holds the userID@serverID form (see NewDBService's
// doc comment).
func (ds *DBService) CreatePendingReedEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName EventName, authorUserID, reedID string) error {
	requesterIdentity := identity.IdentityID(requesterUserID)
	authorIdentity := identity.IdentityID(authorUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requesterIdentity, eventName)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_reed_events (event_id, user_id, reed_id)
		VALUES ($1, $2, $3)
	`, eventID, authorIdentity, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreatePendingAccountEvent inserts pending_events + pending_account_events.
// pending_account_events.user_id is a direct FK to identities(id).
func (ds *DBService) CreatePendingAccountEvent(ctx context.Context, eventID, requestID, requesterUserID, removedUserID string) error {
	requesterIdentity := identity.IdentityID(requesterUserID)
	removedIdentity := identity.IdentityID(removedUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requesterIdentity, AccountRemovedEvent)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_account_events (event_id, user_id)
		VALUES ($1, $2)
	`, eventID, removedIdentity)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreateProfileSubscriptionEvent inserts a reed pending event tied to a profile subscription.
func (ds *DBService) CreateProfileSubscriptionEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName EventName, authorUserID, reedID, subscriptionID string) error {
	requesterIdentity := identity.IdentityID(requesterUserID)
	authorIdentity := identity.IdentityID(authorUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name, subscription_id)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, requestID, requesterIdentity, eventName, subscriptionID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_reed_events (event_id, user_id, reed_id)
		VALUES ($1, $2, $3)
	`, eventID, authorIdentity, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetPendingSubject loads a pending event and its typed child subject by event ID.
// requester_user_id / pending_reed_events.user_id / pending_account_events.user_id
// are scanned into identity.IdentityID and kept in that form (cast to
// string, no .UserID() decode) — see NewDBService's doc comment.
func (ds *DBService) GetPendingSubject(ctx context.Context, eventID string) (*PendingSubject, error) {
	var pe PendingSubject
	var requester identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name
		FROM pending_events pe
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pe.RequesterUserID = string(requester)

	var subjectID identity.IdentityID
	if EventName(pe.EventName) == AccountRemovedEvent {
		err = ds.db.QueryRowContext(ctx, `
			SELECT user_id FROM pending_account_events WHERE event_id = $1
		`, eventID).Scan(&subjectID)
	} else {
		err = ds.db.QueryRowContext(ctx, `
			SELECT user_id, reed_id FROM pending_reed_events WHERE event_id = $1
		`, eventID).Scan(&subjectID, &pe.ReedID)
	}
	if err == nil {
		pe.UserID = string(subjectID)
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
// requester_user_id and pre.user_id are kept in userID@serverID form on
// return (see GetPendingSubject's comment).
func (ds *DBService) GetPendingReedEvent(ctx context.Context, eventID string) (*PendingReedEvent, error) {
	var pe PendingReedEvent
	var requester, author identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &author, &pe.ReedID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pe.RequesterUserID = string(requester)
	pe.UserID = string(author)
	return &pe, nil
}

// DeletePendingEvent deletes a pending event by event ID (cascades to child subject tables).
func (ds *DBService) DeletePendingEvent(ctx context.Context, eventID string) error {
	_, err := ds.db.ExecContext(ctx, `DELETE FROM pending_events WHERE event_id = $1`, eventID)
	return err
}

// DeletePendingEventsByUser deletes all pending events for a given requester user ID
func (ds *DBService) DeletePendingEventsByUser(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `DELETE FROM pending_events WHERE requester_user_id = $1`, selfIdentity)
	return err
}

// DeleteProfileSubscriptionsByViewer deletes all profile subscriptions for a given viewer.
func (ds *DBService) DeleteProfileSubscriptionsByViewer(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `DELETE FROM profile_subscriptions WHERE viewer_user_id = $1`, selfIdentity)
	return err
}

// ReedCoverageTarget identifies a reed whose holder count changed.
type ReedCoverageTarget struct {
	AuthorUserID string
	ReedID       string
}

// AllocateReed records that holderUserID now holds the reed authored by authorUserID.
// Returns true when a new allocation row was inserted. reed_allocations.holder_user_id
// is a direct FK to identities(id); author_user_id is composite-FK'd to reeds(user_id, id).
func (ds *DBService) AllocateReed(ctx context.Context, reedID, holderUserID, authorUserID string) (bool, error) {
	holderIdentity := identity.IdentityID(holderUserID)
	authorIdentity := identity.IdentityID(authorUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, reedID, holderIdentity, authorIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		// coverage.BumpAllocationCount filters reeds.user_id = $2 (an FK'd
		// column) — pass this form, not the bare param.
		if err := coverage.BumpAllocationCount(ctx, tx, string(authorIdentity), reedID, 1); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteReedAllocation removes a single holder's allocation for a reed.
// Returns true when a row was deleted.
func (ds *DBService) DeleteReedAllocation(ctx context.Context, authorUserID, reedID, holderUserID string) (bool, error) {
	authorIdentity := identity.IdentityID(authorUserID)
	holderIdentity := identity.IdentityID(holderUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM reed_allocations
		WHERE author_user_id = $1 AND reed_id = $2 AND holder_user_id = $3
	`, authorIdentity, reedID, holderIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		if err := coverage.BumpAllocationCount(ctx, tx, string(authorIdentity), reedID, -1); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReedExists reports whether a non-removed tip reed row exists for the
// author, and the author's account hasn't itself been removed.
//
// account_removals.user_id / reed_removals.user_id are now written in
// identities.id form (deletion.InsertAccountCert/InsertCert), matching
// r.user_id, so the NOT EXISTS checks correctly match real removal rows.
func (ds *DBService) ReedExists(ctx context.Context, authorUserID, reedID string) (bool, error) {
	authorIdentity := identity.IdentityID(authorUserID)
	var exists bool
	err := ds.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM reeds r
			WHERE r.user_id = $1 AND r.id = $2
			  AND NOT EXISTS (
			    SELECT 1 FROM reed_removals rr
			    WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
			  )
			  AND NOT EXISTS (
			    SELECT 1 FROM account_removals ar WHERE ar.user_id = r.user_id
			  )
		)
	`, authorIdentity, reedID).Scan(&exists)
	return exists, err
}

// GetReedCoverage returns holder count and network coverage percent for a tip reed.
func (ds *DBService) GetReedCoverage(ctx context.Context, authorUserID, reedID string) (holders, percent int, err error) {
	authorIdentity := identity.IdentityID(authorUserID)
	err = ds.db.QueryRowContext(ctx, `
		SELECT allocation_count FROM reeds WHERE user_id = $1 AND id = $2
	`, authorIdentity, reedID).Scan(&holders)
	if err != nil {
		return 0, 0, err
	}
	activeUsers, err := coverage.ActiveUsers(ctx, ds.db)
	if err != nil {
		return 0, 0, err
	}
	return holders, coverage.Percent(holders, activeUsers), nil
}

// GetReedCoveragePercent returns network coverage percent for a tip reed.
func (ds *DBService) GetReedCoveragePercent(ctx context.Context, authorUserID, reedID string) (percent int, err error) {
	_, percent, err = ds.GetReedCoverage(ctx, authorUserID, reedID)
	return percent, err
}

// CountEchoes returns how many non-removed echoes point at the given reed.
//
// No conversion for echoedUserID/echoedReedID: reed_echoes.echoed_user_id/
// echoed_reed_id have no FK and are written bare at insert time.
func (ds *DBService) CountEchoes(ctx context.Context, echoedUserID, echoedReedID string) (int, error) {
	var n int
	err := ds.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT echoing_user_id) FROM reed_echoes
		WHERE echoed_user_id = $1 AND echoed_reed_id = $2
		AND echoing_user_id != echoed_user_id
	`, echoedUserID, echoedReedID).Scan(&n)
	return n, err
}

// CountLikes returns the current like count for a reed, read from the
// denormalized reeds.like_count column.
func (ds *DBService) CountLikes(ctx context.Context, authorUserID, reedID string) (int, error) {
	authorIdentity := identity.IdentityID(authorUserID)
	var n int
	err := ds.db.QueryRowContext(ctx, `
		SELECT like_count FROM reeds WHERE user_id = $1 AND id = $2
	`, authorIdentity, reedID).Scan(&n)
	return n, err
}

// GetReedStatsSnapshot returns echoes, coverage, subtree reply count, and
// like count for subscribe ACK.
func (ds *DBService) GetReedStatsSnapshot(ctx context.Context, authorUserID, reedID string) (echoes, coveragePercent, replies, likes int, err error) {
	coveragePercent, err = ds.GetReedCoveragePercent(ctx, authorUserID, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	echoes, err = ds.CountEchoes(ctx, authorUserID, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	replies, err = ds.GetSubtreeReplyCount(ctx, authorUserID, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	likes, err = ds.CountLikes(ctx, authorUserID, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return echoes, coveragePercent, replies, likes, nil
}

// ReplyParent returns the immediate (parent_user_id, parent_reed_id) that
// (userID, reedID) replies to, if it's indexed as a reply at all — ok is
// false when it isn't. reed_replies.user_id/parent_user_id are both
// composite-FK'd to reeds(user_id, id).
func (ds *DBService) ReplyParent(ctx context.Context, userID, reedID string) (parentUserID, parentReedID string, ok bool, err error) {
	selfIdentity := identity.IdentityID(userID)
	var parentIdentity identity.IdentityID
	err = ds.db.QueryRowContext(ctx, `
		SELECT parent_user_id, parent_reed_id
		FROM reed_replies
		WHERE user_id = $1 AND reed_id = $2
	`, selfIdentity, reedID).Scan(&parentIdentity, &parentReedID)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return string(parentIdentity), parentReedID, true, nil
}

// GetSubtreeReplyCount returns live descendant reply count beneath userID/reedID.
// Matches services.go's identical GetSubtreeReplyCount: only the top-level
// parent_user_id bind param needs conversion.
func (ds *DBService) GetSubtreeReplyCount(ctx context.Context, userID, reedID string) (int, error) {
	selfIdentity := identity.IdentityID(userID)
	var count int
	err := ds.db.QueryRowContext(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT rr.user_id, rr.reed_id
			FROM reed_replies rr
			WHERE rr.parent_user_id = $1 AND rr.parent_reed_id = $2
			AND NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.user_id = rr.user_id AND rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar WHERE ar.user_id = rr.user_id
			)
			UNION ALL
			SELECT rr.user_id, rr.reed_id
			FROM reed_replies rr
			INNER JOIN descendants d
				ON rr.parent_user_id = d.user_id AND rr.parent_reed_id = d.reed_id
			WHERE NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.user_id = rr.user_id AND rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar WHERE ar.user_id = rr.user_id
			)
		)
		SELECT COUNT(*) FROM descendants
	`, selfIdentity, reedID).Scan(&count)
	return count, err
}

// GetNextPendingForHolder returns the oldest undispatched reed pending for reeds held by holderUserID.
func (ds *DBService) GetNextPendingForHolder(ctx context.Context, holderUserID string) (*PendingReedEvent, error) {
	holderIdentity := identity.IdentityID(holderUserID)
	var pe PendingReedEvent
	var requester, author identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra
		  ON ra.reed_id = pre.reed_id AND ra.author_user_id = pre.user_id
		WHERE ra.holder_user_id = $1
		  AND pe.dispatched_at IS NULL
		ORDER BY pe.created_at
		LIMIT 1
	`, holderIdentity).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &author, &pe.ReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pe.RequesterUserID = string(requester)
	pe.UserID = string(author)
	return &pe, nil
}

// MarkEventDispatched marks an event as dispatched. Returns true if the update claimed the row
// (i.e. it was still undispatched), false if another replica already claimed it.
func (ds *DBService) MarkEventDispatched(ctx context.Context, eventID string) (bool, error) {
	result, err := ds.db.ExecContext(ctx, `
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
func (ds *DBService) ResetDispatchedAt(ctx context.Context, eventID string) error {
	_, err := ds.db.ExecContext(ctx, `
		UPDATE pending_events SET dispatched_at = NULL WHERE event_id = $1
	`, eventID)
	return err
}

// GetOnlineHolders reports whether a reed has any holders and returns one online holder
// for relay dispatch when available. Callers must delete stale holder rows (e.g. the
// requester) before calling when appropriate.
func (ds *DBService) GetOnlineHolders(ctx context.Context, authorUserID, reedID string) (hasHolders bool, holder string, err error) {
	authorIdentity := identity.IdentityID(authorUserID)
	var onlineHolder sql.NullString
	err = ds.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM reed_allocations
				WHERE author_user_id = $1 AND reed_id = $2
			),
			(
				SELECT ou.user_id
				FROM reed_allocations ra
				JOIN online_users ou ON ou.user_id = ra.holder_user_id
				WHERE ra.author_user_id = $1 AND ra.reed_id = $2
				LIMIT 1
			)
	`, authorIdentity, reedID).Scan(&hasHolders, &onlineHolder)
	if err != nil {
		return false, "", err
	}
	if onlineHolder.Valid {
		holder = onlineHolder.String
	}
	return hasHolders, holder, nil
}

// GetOnlineReedHolder returns the user ID of one online holder of the given reed,
// or an empty string if no holder is currently online.
func (ds *DBService) GetOnlineReedHolder(ctx context.Context, authorUserID, reedID string) (string, error) {
	authorIdentity := identity.IdentityID(authorUserID)
	var userID identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.holder_user_id = ou.user_id
		WHERE ra.author_user_id = $1 AND ra.reed_id = $2
		LIMIT 1
	`, authorIdentity, reedID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(userID), nil
}

// ClaimPendingFanout removes the pending_fanout row if present. Returns true when
// this call claimed fanout (row deleted), plus any pipe tags stashed at SignReed.
// Concurrent READY messages only claim once. pending_fanout.user_id is
// composite-FK'd to reeds(user_id, id).
func (ds *DBService) ClaimPendingFanout(ctx context.Context, authorUserID, reedID string) (claimed bool, tags []string, err error) {
	authorIdentity := identity.IdentityID(authorUserID)
	var id string
	var tagArray pq.StringArray
	err = ds.db.QueryRowContext(ctx, `
		DELETE FROM pending_fanout
		WHERE user_id = $1 AND reed_id = $2
		RETURNING reed_id, tags
	`, authorIdentity, reedID).Scan(&id, &tagArray)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, []string(tagArray), nil
}

// GetPendingEventsForUser returns all pending reed events for reeds held by the given user.
func (ds *DBService) GetPendingEventsForUser(ctx context.Context, userID string) ([]PendingReedEvent, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra
		  ON ra.reed_id = pre.reed_id AND ra.author_user_id = pre.user_id
		WHERE ra.holder_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedEvent
	for rows.Next() {
		var prr PendingReedEvent
		var requester, author identity.IdentityID
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&author,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = string(requester)
		prr.UserID = string(author)
		results = append(results, prr)
	}
	return results, nil
}

// GetPendingRequestsForRequester returns pending reed events initiated by the given user
// (reed relay retry only — not account events).
func (ds *DBService) GetPendingRequestsForRequester(ctx context.Context, requesterUserID string) ([]PendingReedEvent, error) {
	requesterIdentity := identity.IdentityID(requesterUserID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.user_id, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		WHERE pe.requester_user_id = $1
	`, requesterIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedEvent
	for rows.Next() {
		var prr PendingReedEvent
		var requester, author identity.IdentityID
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&author,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = string(requester)
		prr.UserID = string(author)
		results = append(results, prr)
	}
	return results, nil
}

// GetMissingReedIDsForViewer returns IDs of reeds by authorID that viewerID does not yet have,
// excluding any IDs the viewer already holds locally (ownedIDs) or via reed_allocations.
func (ds *DBService) GetMissingReedIDsForViewer(ctx context.Context, authorID, viewerID string, ownedIDs []string) ([]string, error) {
	if ownedIDs == nil {
		ownedIDs = []string{}
	}
	authorIdentity := identity.IdentityID(authorID)
	viewerIdentity := identity.IdentityID(viewerID)
	rows, err := ds.db.QueryContext(ctx, `
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
	`, authorIdentity, viewerIdentity, pq.Array(ownedIDs))
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
// user_following.user_id/following_user_id are both direct FKs to identities(id).
func (ds *DBService) GetMissingOut(ctx context.Context, userID string) ([]UnallocatedReed, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
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
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UnallocatedReed
	for rows.Next() {
		var reedID string
		var authorIdentity identity.IdentityID
		if err := rows.Scan(&reedID, &authorIdentity); err != nil {
			return nil, err
		}
		results = append(results, UnallocatedReed{ReedID: reedID, AuthorID: string(authorIdentity)})
	}
	return results, nil
}

// GetUnallocatedReeds returns IDs of reeds by authorID that viewerID does not have in reed_allocations.
func (ds *DBService) GetUnallocatedReeds(ctx context.Context, authorID, viewerID string) ([]string, error) {
	authorIdentity := identity.IdentityID(authorID)
	viewerIdentity := identity.IdentityID(viewerID)
	rows, err := ds.db.QueryContext(ctx, `
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
	`, authorIdentity, viewerIdentity)
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
// profile_subscriptions.viewer_user_id/author_user_id are both direct FKs to identities(id).
func (ds *DBService) CreateProfileSubscription(ctx context.Context, subscriptionID, viewerUserID, authorUserID string) error {
	viewerIdentity := identity.IdentityID(viewerUserID)
	authorIdentity := identity.IdentityID(authorUserID)
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO profile_subscriptions (subscription_id, viewer_user_id, author_user_id)
		VALUES ($1, $2, $3)
	`, subscriptionID, viewerIdentity, authorIdentity)
	return err
}

// GetProfileSubscription returns the subscription ID for an active (viewer, author) pair.
// Returns an empty string when no subscription exists.
func (ds *DBService) GetProfileSubscription(ctx context.Context, viewerUserID, authorUserID string) (string, error) {
	viewerIdentity := identity.IdentityID(viewerUserID)
	authorIdentity := identity.IdentityID(authorUserID)
	var id string
	err := ds.db.QueryRowContext(ctx, `
		SELECT subscription_id FROM profile_subscriptions
		WHERE viewer_user_id = $1 AND author_user_id = $2
	`, viewerIdentity, authorIdentity).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// DeleteProfileSubscription deletes a subscription by ID, cascading to its pending_events.
func (ds *DBService) DeleteProfileSubscription(ctx context.Context, subscriptionID string) error {
	_, err := ds.db.ExecContext(ctx, `
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
func (ds *DBService) GetProfileSubscribers(ctx context.Context, authorID string) ([]ProfileSubscriber, error) {
	authorIdentity := identity.IdentityID(authorID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT subscription_id, viewer_user_id
		FROM profile_subscriptions
		WHERE author_user_id = $1
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []ProfileSubscriber
	for rows.Next() {
		var subscriber ProfileSubscriber
		var viewer identity.IdentityID
		if err := rows.Scan(&subscriber.SubscriptionID, &viewer); err != nil {
			return nil, err
		}
		subscriber.ViewerUserID = string(viewer)
		subscribers = append(subscribers, subscriber)
	}
	return subscribers, nil
}

// GetBroadcastSubscribers returns up to 100 broadcast subscribers for the given author,
// throttled to one delivery per second per subscriber.
// Followers of the author are excluded — they receive the reed via the follow
// path (follow_reed → followcast), not as ephemeral broadcast.
// Subscribers are selected in order of oldest last_delivery (NULLS FIRST) and their
// last_delivery timestamp is updated atomically.
//
// NOTE: This UPDATE is not serialised across replicas. Two concurrent replicas could select
// the same batch before either writes last_delivery, causing up to 100 users to receive a
// duplicate. At our current replica count this is acceptable — duplicates are harmless (the
// client deduplicates by reed ID) and the race window is tiny.
//
// authorID is converted once, up front, and reused everywhere $1 appears
// so the != and = comparisons agree.
func (ds *DBService) GetBroadcastSubscribers(ctx context.Context, authorID string) ([]string, error) {
	authorIdentity := identity.IdentityID(authorID)
	rows, err := ds.db.QueryContext(ctx, `
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
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []string
	for rows.Next() {
		var userID identity.IdentityID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, string(userID))
	}

	return subscribers, nil
}

// MissingRemoval is a reed_allocations ∩ reed_removals row for catch-up.
type MissingRemoval struct {
	ReedID string
	UserID string
	Cert   ReedRemovalWire
}

// GetMissingRemovals returns removal certs for reeds this user still holds.
// deletion.GetCert takes a bare userID + serverID, so authorIdentity is
// split via .UserID() only for that call; MissingRemoval.UserID stays in
// userID@serverID form, from cert.UserID.
func (ds *DBService) GetMissingRemovals(ctx context.Context, userID string) ([]MissingRemoval, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT rr.reed_id, rr.user_id
		FROM reed_allocations ra
		JOIN reed_removals rr
		  ON rr.reed_id = ra.reed_id AND rr.user_id = ra.author_user_id
		WHERE ra.holder_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	serverID := ds.serverID

	var out []MissingRemoval
	for rows.Next() {
		var reedID string
		var authorIdentity identity.IdentityID
		if err := rows.Scan(&reedID, &authorIdentity); err != nil {
			return nil, err
		}
		cert, err := deletion.GetCert(ctx, ds.db, authorIdentity.UserID(), reedID, serverID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, MissingRemoval{
			ReedID: reedID,
			UserID: cert.UserID,
			Cert:   NewReedRemovalWire(serverID, cert),
		})
	}
	return out, rows.Err()
}

// GetReedRemovalWire loads a removal cert for WS delivery. authorID arrives
// in userID@serverID form (see NewDBService's doc comment), but
// deletion.GetCert takes a bare userID + serverID — decoded here.
func (ds *DBService) GetReedRemovalWire(ctx context.Context, authorID, reedID string) (ReedRemovalWire, error) {
	cert, err := deletion.GetCert(ctx, ds.db, identity.IdentityID(authorID).UserID(), reedID, ds.serverID)
	if err != nil || cert == nil {
		return ReedRemovalWire{}, err
	}
	return NewReedRemovalWire(ds.serverID, cert), nil
}

// MissingAccountRemoval is a catch-up row: viewer still follows or holds
// allocations for a removed author's reeds.
type MissingAccountRemoval struct {
	UserID string
	Cert   AccountRemovalWire
}

// GetMissingAccountRemovals returns account_removals that still apply to viewer
// (follow ∪ allocations for that author's reeds).
//
// uf.user_id/uf.following_user_id and ra.holder_user_id are direct FKs to
// identities(id); the $1 bind param (viewerUserID) is converted.
// deletion.InsertAccountCert now writes account_removals.user_id in the
// same form, so the EXISTS subqueries above match real removal rows.
func (ds *DBService) GetMissingAccountRemovals(ctx context.Context, viewerUserID string) ([]MissingAccountRemoval, error) {
	selfIdentity := identity.IdentityID(viewerUserID)
	serverID := ds.serverID
	rows, err := ds.db.QueryContext(ctx, `
		SELECT ar.user_id
		FROM account_removals ar
		WHERE EXISTS (
			SELECT 1 FROM user_following uf
			WHERE uf.user_id = $1 AND uf.following_user_id = ar.user_id
		) OR EXISTS (
			SELECT 1 FROM reed_allocations ra
			WHERE ra.holder_user_id = $1 AND ra.author_user_id = ar.user_id
		)
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MissingAccountRemoval
	for rows.Next() {
		var removedIdentity identity.IdentityID
		if err := rows.Scan(&removedIdentity); err != nil {
			return nil, err
		}
		// deletion.GetAccountCert takes a bare userID + serverID — decode
		// only for this call; MissingAccountRemoval.UserID stays in
		// userID@serverID form, sourced from cert.UserID.
		cert, err := deletion.GetAccountCert(ctx, ds.db, removedIdentity.UserID(), serverID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, MissingAccountRemoval{
			UserID: cert.UserID,
			Cert:   NewAccountRemovalWire(serverID, cert),
		})
	}
	return out, rows.Err()
}

// GetAccountRemovalWire loads an account-removal cert for WS delivery.
// userID arrives in userID@serverID form (see NewDBService's doc comment);
// deletion.GetAccountCert takes a bare userID + serverID — decoded here.
func (ds *DBService) GetAccountRemovalWire(ctx context.Context, userID string) (AccountRemovalWire, error) {
	cert, err := deletion.GetAccountCert(ctx, ds.db, identity.IdentityID(userID).UserID(), ds.serverID)
	if err != nil || cert == nil {
		return AccountRemovalWire{}, err
	}
	return NewAccountRemovalWire(ds.serverID, cert), nil
}

// ClearPeerStateForRemovedAccount drops follow edges and allocations so
// catch-up no longer re-delivers the account cert to this viewer.
// Returns reeds whose holder counts changed.
//
// All 6 tables touched here are FK'd, directly or transitively, to
// identities(id); viewerUserID and removedUserID are converted once, up
// front, and used consistently across every statement in the transaction.
func (ds *DBService) ClearPeerStateForRemovedAccount(ctx context.Context, viewerUserID, removedUserID string) ([]ReedCoverageTarget, error) {
	viewerIdentity := identity.IdentityID(viewerUserID)
	removedIdentity := identity.IdentityID(removedUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmts := []struct {
		q  string
		a1 identity.IdentityID
		a2 identity.IdentityID
	}{
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, viewerIdentity, removedIdentity},
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, removedIdentity, viewerIdentity},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, removedIdentity, viewerIdentity},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, viewerIdentity, removedIdentity},
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.q, s.a1, s.a2); err != nil {
			return nil, err
		}
	}

	rows, err := tx.QueryContext(ctx, `
		WITH deleted AS (
			DELETE FROM reed_allocations
			WHERE holder_user_id = $1 AND author_user_id = $2
			RETURNING author_user_id, reed_id
		),
		counts AS (
			SELECT author_user_id, reed_id, COUNT(*) AS cnt
			FROM deleted
			GROUP BY author_user_id, reed_id
		),
		updated AS (
			UPDATE reeds r
			SET allocation_count = GREATEST(0, r.allocation_count - c.cnt)
			FROM counts c
			WHERE r.user_id = c.author_user_id AND r.id = c.reed_id
			RETURNING c.author_user_id, c.reed_id
		)
		SELECT author_user_id, reed_id FROM updated
	`, viewerIdentity, removedIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ReedCoverageTarget
	for rows.Next() {
		var t ReedCoverageTarget
		var author identity.IdentityID
		if err := rows.Scan(&author, &t.ReedID); err != nil {
			return nil, err
		}
		t.AuthorUserID = string(author)
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return targets, nil
}
