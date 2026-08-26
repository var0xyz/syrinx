package realtime

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

// GetUserPublicKey retrieves a user's public key by canonical, self-scoping
// fingerprint — same shape as DataService.GetPublicKey in the main package.
func (ds *DBService) GetUserPublicKey(ctx context.Context, fingerprint string) (string, error) {
	var armor string
	err := ds.db.QueryRowContext(ctx, `
		SELECT armor
		FROM public_keys
		WHERE id = $1
	`, fingerprint).Scan(&armor)

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
func (ds *DBService) CreatePendingReedEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName EventName, reedID string) error {
	requesterIdentity := identity.IdentityID(requesterUserID)

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
		INSERT INTO pending_reed_events (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
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
func (ds *DBService) CreateProfileSubscriptionEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName EventName, reedID, subscriptionID string) error {
	requesterIdentity := identity.IdentityID(requesterUserID)

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
		INSERT INTO pending_reed_events (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
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
		if err == nil {
			pe.UserID = string(subjectID)
		}
	} else {
		err = ds.db.QueryRowContext(ctx, `
			SELECT reed_id FROM pending_reed_events WHERE event_id = $1
		`, eventID).Scan(&pe.ReedID)
		if err == nil {
			pe.UserID = reedAuthorIdentity(pe.ReedID)
		}
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
// requester_user_id is kept in userID@serverID form on return (see
// GetPendingSubject's comment); pe.UserID (author) is derived from the
// canonical reed_id.
func (ds *DBService) GetPendingReedEvent(ctx context.Context, eventID string) (*PendingReedEvent, error) {
	var pe PendingReedEvent
	var requester identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &pe.ReedID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pe.RequesterUserID = string(requester)
	pe.UserID = reedAuthorIdentity(pe.ReedID)
	return &pe, nil
}

// reedAuthorIdentity extracts the userID@serverID author identity embedded
// in a canonical reed id. Empty string if reedID is malformed.
func reedAuthorIdentity(reedID string) string {
	userID, serverID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		return ""
	}
	return string(identity.CanonicalID(serverID, userID))
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

// ViewerSubscription is one of a viewer's own active profile subscriptions
// (the reverse of ProfileSubscriber, which is keyed by author instead).
type ViewerSubscription struct {
	SubscriptionID string
	AuthorUserID   string
}

// GetProfileSubscriptionsByViewer lists a viewer's active subscriptions —
// used on disconnect to notify any foreign authors' home servers before
// DeleteProfileSubscriptionsByViewer removes the local rows.
func (ds *DBService) GetProfileSubscriptionsByViewer(ctx context.Context, userID string) ([]ViewerSubscription, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT subscription_id, author_user_id
		FROM profile_subscriptions
		WHERE viewer_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []ViewerSubscription
	for rows.Next() {
		var sub ViewerSubscription
		var author identity.IdentityID
		if err := rows.Scan(&sub.SubscriptionID, &author); err != nil {
			return nil, err
		}
		sub.AuthorUserID = string(author)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// ReedCoverageTarget identifies a reed whose holder count changed.
type ReedCoverageTarget struct {
	AuthorUserID string
	ReedID       string
}

// AllocateReed records that holderUserID now holds reedID. Returns true
// when a new allocation row was inserted. holderUserID must be a genuine
// local user — reed_allocations.holder_user_id is a direct FK to users(id).
// For a peer server (not a specific user) reporting it holds a copy, see
// RecordServerHolder instead.
func (ds *DBService) AllocateReed(ctx context.Context, reedID, holderUserID string) (bool, error) {
	holderIdentity := identity.IdentityID(holderUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, reedID, holderIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		if err := coverage.BumpAllocationCount(ctx, tx, reedID, 1); err != nil {
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
func (ds *DBService) DeleteReedAllocation(ctx context.Context, reedID, holderUserID string) (bool, error) {
	holderIdentity := identity.IdentityID(holderUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM reed_allocations
		WHERE reed_id = $1 AND holder_user_id = $2
	`, reedID, holderIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		if err := coverage.BumpAllocationCount(ctx, tx, reedID, -1); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReedExists reports whether a non-removed tip reed row exists for reedID,
// and the author's account hasn't itself been removed.
func (ds *DBService) ReedExists(ctx context.Context, reedID string) (bool, error) {
	var exists bool
	err := ds.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM reeds r
			WHERE r.id = $1
			  AND NOT EXISTS (
			    SELECT 1 FROM reed_removals rr
			    WHERE rr.reed_id = r.id
			  )
			  AND NOT EXISTS (
			    SELECT 1 FROM account_removals ar WHERE ar.user_id = r.user_id
			  )
		)
	`, reedID).Scan(&exists)
	return exists, err
}

// GetReedCoverage returns holder count and network coverage percent for a tip reed.
func (ds *DBService) GetReedCoverage(ctx context.Context, reedID string) (holders, percent int, err error) {
	err = ds.db.QueryRowContext(ctx, `
		SELECT allocation_count FROM reeds WHERE id = $1
	`, reedID).Scan(&holders)
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
func (ds *DBService) GetReedCoveragePercent(ctx context.Context, reedID string) (percent int, err error) {
	_, percent, err = ds.GetReedCoverage(ctx, reedID)
	return percent, err
}

// CountEchoes returns how many non-removed echoes point at the given reed.
// echoing_author_id is stored directly on reed_echoes (same shape as
// services.go's CountEchoes) — no join against reeds, which would only
// ever match a local echoer and silently exclude a foreign one.
func (ds *DBService) CountEchoes(ctx context.Context, echoedReedID string) (int, error) {
	var n int
	err := ds.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT echoing_author_id) FROM reed_echoes
		WHERE echoed_reed_id = $1
		AND echoing_reed_id != echoed_reed_id
	`, echoedReedID).Scan(&n)
	return n, err
}

// CountLikes returns the current like count for a reed, read from the
// denormalized reeds.like_count column.
func (ds *DBService) CountLikes(ctx context.Context, reedID string) (int, error) {
	var n int
	err := ds.db.QueryRowContext(ctx, `
		SELECT like_count FROM reeds WHERE id = $1
	`, reedID).Scan(&n)
	return n, err
}

// GetReedStatsSnapshot returns echoes, coverage, subtree reply count, and
// like count for subscribe ACK.
func (ds *DBService) GetReedStatsSnapshot(ctx context.Context, reedID string) (echoes, coveragePercent, replies, likes int, err error) {
	coveragePercent, err = ds.GetReedCoveragePercent(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	echoes, err = ds.CountEchoes(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	replies, err = ds.GetSubtreeReplyCount(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	likes, err = ds.CountLikes(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return echoes, coveragePercent, replies, likes, nil
}

// ReplyParent returns the immediate parent_reed_id that reedID replies to,
// if it's indexed as a reply at all — ok is false when it isn't.
func (ds *DBService) ReplyParent(ctx context.Context, reedID string) (parentReedID string, ok bool, err error) {
	err = ds.db.QueryRowContext(ctx, `
		SELECT parent_reed_id
		FROM reed_replies
		WHERE reed_id = $1
	`, reedID).Scan(&parentReedID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return parentReedID, true, nil
}

// ReplyRecord is one reed's full reed_replies row.
type ReplyRecord struct {
	ParentReedID string
	ThreadID     string
	Timestamp    time.Time
}

// GetReplyRecord loads reedID's own reed_replies row (parent + thread +
// timestamp in one query) — used to notify a foreign parent's home
// server of the reply once, rather than three separate lookups.
func (ds *DBService) GetReplyRecord(ctx context.Context, reedID string) (*ReplyRecord, error) {
	var rec ReplyRecord
	err := ds.db.QueryRowContext(ctx, `
		SELECT parent_reed_id, thread_id, timestamp
		FROM reed_replies
		WHERE reed_id = $1
	`, reedID).Scan(&rec.ParentReedID, &rec.ThreadID, &rec.Timestamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// InsertForeignReply records that a peer-authored reedID replies to
// parentReedID (local to this server) — the home-server side of the
// reply-notify leg. Idempotent (ON CONFLICT DO NOTHING on reed_id, the
// table's PK): a retried notify is a harmless no-op, not a duplicate.
// Upserts a reed_identities row for the foreign reply reedID first, the
// same "legitimate reference" bar every other foreign-reed reference in
// this server uses.
func (ds *DBService) InsertForeignReply(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error {
	_, replyServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(replyReedID))
	if !ok {
		return fmt.Errorf("malformed reply reed id: %s", replyReedID)
	}
	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, replyReedID, replyServerID); err != nil {
		return fmt.Errorf("insert foreign reply reed identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_replies (thread_id, reed_id, parent_reed_id, timestamp)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (reed_id) DO NOTHING
	`, threadID, replyReedID, parentReedID, ts.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("insert foreign reply: %w", err)
	}
	return tx.Commit()
}

// GetSubtreeReplyCount returns live descendant reply count beneath reedID.
// Matches services.go's identical GetSubtreeReplyCount.
func (ds *DBService) GetSubtreeReplyCount(ctx context.Context, reedID string) (int, error) {
	var count int
	err := ds.db.QueryRowContext(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT rr.reed_id
			FROM reed_replies rr
			WHERE rr.parent_reed_id = $1
			AND NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar
				JOIN reeds r ON r.id = rr.reed_id
				WHERE ar.user_id = r.user_id
			)
			UNION ALL
			SELECT rr.reed_id
			FROM reed_replies rr
			INNER JOIN descendants d
				ON rr.parent_reed_id = d.reed_id
			WHERE NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar
				JOIN reeds r ON r.id = rr.reed_id
				WHERE ar.user_id = r.user_id
			)
		)
		SELECT COUNT(*) FROM descendants
	`, reedID).Scan(&count)
	return count, err
}

// GetNextPendingForHolder returns the oldest undispatched reed pending for reeds held by holderUserID.
func (ds *DBService) GetNextPendingForHolder(ctx context.Context, holderUserID string) (*PendingReedEvent, error) {
	holderIdentity := identity.IdentityID(holderUserID)
	var pe PendingReedEvent
	var requester identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra ON ra.reed_id = pre.reed_id
		WHERE ra.holder_user_id = $1
		  AND pe.dispatched_at IS NULL
		ORDER BY pe.created_at
		LIMIT 1
	`, holderIdentity).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &pe.ReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pe.RequesterUserID = string(requester)
	pe.UserID = reedAuthorIdentity(pe.ReedID)
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
func (ds *DBService) GetOnlineHolders(ctx context.Context, reedID string) (hasHolders bool, holder string, err error) {
	var onlineHolder sql.NullString
	err = ds.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM reed_allocations WHERE reed_id = $1
			),
			(
				SELECT ou.user_id
				FROM reed_allocations ra
				JOIN online_users ou ON ou.user_id = ra.holder_user_id
				WHERE ra.reed_id = $1
				LIMIT 1
			)
	`, reedID).Scan(&hasHolders, &onlineHolder)
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
func (ds *DBService) GetOnlineReedHolder(ctx context.Context, reedID string) (string, error) {
	var userID identity.IdentityID
	err := ds.db.QueryRowContext(ctx, `
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.holder_user_id = ou.user_id
		WHERE ra.reed_id = $1
		LIMIT 1
	`, reedID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(userID), nil
}

// RecordServerHolder upserts "peer server serverID holds a copy of
// reedID," idempotent per (reed_id, server_id) — multiple users on the
// same peer holding the same reed collapse to one row, since the fallback
// only ever delegates to the peer server as a whole, never a specific
// foreign user.
func (ds *DBService) RecordServerHolder(ctx context.Context, reedID, serverID string) error {
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO reed_server_allocations (reed_id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (reed_id, server_id) DO NOTHING
	`, reedID, serverID)
	return err
}

// GetForeignHolderServers returns peer server IDs known to hold a copy of
// reedID, oldest-recorded-first, capped so a widely-relayed reed can't
// blow up a sequential fallback loop's latency.
func (ds *DBService) GetForeignHolderServers(ctx context.Context, reedID string) ([]string, error) {
	rows, err := ds.db.QueryContext(ctx, `
		SELECT server_id FROM reed_server_allocations
		WHERE reed_id = $1
		ORDER BY delivered_at ASC
		LIMIT 5
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var serverIDs []string
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			return nil, err
		}
		serverIDs = append(serverIDs, serverID)
	}
	return serverIDs, rows.Err()
}

// ClaimPendingFanout removes the pending_fanout row if present. Returns true when
// this call claimed fanout (row deleted), plus any pipe tags stashed at SignReed.
// Concurrent READY messages only claim once. pending_fanout.reed_id FKs to reeds(id).
func (ds *DBService) ClaimPendingFanout(ctx context.Context, reedID string) (claimed bool, tags []string, err error) {
	var id string
	var tagArray pq.StringArray
	err = ds.db.QueryRowContext(ctx, `
		DELETE FROM pending_fanout
		WHERE reed_id = $1
		RETURNING reed_id, tags
	`, reedID).Scan(&id, &tagArray)
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
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra ON ra.reed_id = pre.reed_id
		WHERE ra.holder_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingReedEvent
	for rows.Next() {
		var prr PendingReedEvent
		var requester identity.IdentityID
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = string(requester)
		prr.UserID = reedAuthorIdentity(prr.ReedID)
		results = append(results, prr)
	}
	return results, nil
}

// GetPendingRequestsForRequester returns pending reed events initiated by the given user
// (reed relay retry only — not account events).
func (ds *DBService) GetPendingRequestsForRequester(ctx context.Context, requesterUserID string) ([]PendingReedEvent, error) {
	requesterIdentity := identity.IdentityID(requesterUserID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
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
		var requester identity.IdentityID
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = string(requester)
		prr.UserID = reedAuthorIdentity(prr.ReedID)
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
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
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
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $1
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
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
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
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

// GetUnallocatedReedsForServer is GetUnallocatedReeds' server-scoped
// counterpart: used where the "viewer" is a whole peer server rather than
// a genuine local user, since reed_server_allocations has no per-user
// granularity — a peer's sentinel identity is never a valid holder_user_id
// in reed_allocations.
func (ds *DBService) GetUnallocatedReedsForServer(ctx context.Context, authorID, serverID string) ([]string, error) {
	authorIdentity := identity.IdentityID(authorID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_server_allocations rsa
		      WHERE rsa.reed_id = r.id AND rsa.server_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
	`, authorIdentity, serverID)
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

// CreateProfileSubscription records an active profile feed subscription for
// a viewer, returning the effective subscription_id. Idempotent per
// (viewer_user_id, author_user_id): a repeat subscribe for a pair that's
// already active (e.g. revisiting the same profile page) reuses the
// existing subscription_id instead of inserting a second row —
// GetProfileSubscribers has no DISTINCT, so duplicate rows for the same
// viewer would otherwise fan out the same new-reed event to them more than
// once. Callers must use the returned id (not the one they passed in) since
// a conflict discards the caller's candidate in favor of the existing row.
// profile_subscriptions.viewer_user_id/author_user_id are both direct FKs to identities(id).
func (ds *DBService) CreateProfileSubscription(ctx context.Context, subscriptionID, viewerUserID, authorUserID string) (string, error) {
	viewerIdentity := identity.IdentityID(viewerUserID)
	authorIdentity := identity.IdentityID(authorUserID)
	var effectiveID string
	err := ds.db.QueryRowContext(ctx, `
		INSERT INTO profile_subscriptions (subscription_id, viewer_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (viewer_user_id, author_user_id)
			DO UPDATE SET viewer_user_id = EXCLUDED.viewer_user_id
		RETURNING subscription_id
	`, subscriptionID, viewerIdentity, authorIdentity).Scan(&effectiveID)
	return effectiveID, err
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

// CreateReedSubscription records an active reed-stats subscription for a
// viewer. reed_subscriptions.reed_id FKs to reed_identities (not reeds
// directly), so the subscribed reed may be local or foreign.
func (ds *DBService) CreateReedSubscription(ctx context.Context, subscriptionID, viewerUserID, reedID string) error {
	viewerIdentity := identity.IdentityID(viewerUserID)
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO reed_subscriptions (subscription_id, viewer_user_id, reed_id)
		VALUES ($1, $2, $3)
	`, subscriptionID, viewerIdentity, reedID)
	return err
}

// GetReedSubscription returns the subscription ID for an active (viewer,
// reed) pair. Returns an empty string when no subscription exists.
func (ds *DBService) GetReedSubscription(ctx context.Context, viewerUserID, reedID string) (string, error) {
	viewerIdentity := identity.IdentityID(viewerUserID)
	var id string
	err := ds.db.QueryRowContext(ctx, `
		SELECT subscription_id FROM reed_subscriptions
		WHERE viewer_user_id = $1 AND reed_id = $2
	`, viewerIdentity, reedID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// DeleteReedSubscription deletes a reed-stats subscription by ID.
func (ds *DBService) DeleteReedSubscription(ctx context.Context, subscriptionID string) error {
	_, err := ds.db.ExecContext(ctx, `
		DELETE FROM reed_subscriptions WHERE subscription_id = $1
	`, subscriptionID)
	return err
}

// DeleteReedSubscriptionsByViewer deletes all reed-stats subscriptions for a given viewer.
func (ds *DBService) DeleteReedSubscriptionsByViewer(ctx context.Context, userID string) error {
	selfIdentity := identity.IdentityID(userID)
	_, err := ds.db.ExecContext(ctx, `DELETE FROM reed_subscriptions WHERE viewer_user_id = $1`, selfIdentity)
	return err
}

// ReedSubscriber represents an active reed-stats subscription.
type ReedSubscriber struct {
	SubscriptionID string
	ViewerUserID   string
}

// GetReedSubscribers returns all active reed-stats subscriptions for the given reed.
func (ds *DBService) GetReedSubscribers(ctx context.Context, reedID string) ([]ReedSubscriber, error) {
	rows, err := ds.db.QueryContext(ctx, `
		SELECT subscription_id, viewer_user_id
		FROM reed_subscriptions
		WHERE reed_id = $1
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []ReedSubscriber
	for rows.Next() {
		var sub ReedSubscriber
		var viewer identity.IdentityID
		if err := rows.Scan(&sub.SubscriptionID, &viewer); err != nil {
			return nil, err
		}
		sub.ViewerUserID = string(viewer)
		subscribers = append(subscribers, sub)
	}
	return subscribers, rows.Err()
}

// ViewerReedSubscription is one of a viewer's own active reed-stats
// subscriptions (the reverse of ReedSubscriber, which is keyed by reed).
type ViewerReedSubscription struct {
	SubscriptionID string
	ReedID         string
}

// GetReedSubscriptionsByViewer lists a viewer's active reed-stats
// subscriptions — used on disconnect to notify any foreign reeds' home
// servers before DeleteReedSubscriptionsByViewer removes the local rows.
func (ds *DBService) GetReedSubscriptionsByViewer(ctx context.Context, userID string) ([]ViewerReedSubscription, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT subscription_id, reed_id
		FROM reed_subscriptions
		WHERE viewer_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []ViewerReedSubscription
	for rows.Next() {
		var sub ViewerReedSubscription
		if err := rows.Scan(&sub.SubscriptionID, &sub.ReedID); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
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
// MissingRemoval.UserID comes from cert.UserID, in userID@serverID form.
func (ds *DBService) GetMissingRemovals(ctx context.Context, userID string) ([]MissingRemoval, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT rr.reed_id
		FROM reed_allocations ra
		JOIN reed_removals rr ON rr.reed_id = ra.reed_id
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
		if err := rows.Scan(&reedID); err != nil {
			return nil, err
		}
		cert, err := deletion.GetCert(ctx, ds.db, reedID, serverID)
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

// GetReedRemovalWire loads a removal cert for WS delivery. reedID is canonical.
func (ds *DBService) GetReedRemovalWire(ctx context.Context, reedID string) (ReedRemovalWire, error) {
	cert, err := deletion.GetCert(ctx, ds.db, reedID, ds.serverID)
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
			JOIN reeds r ON r.id = ra.reed_id
			WHERE ra.holder_user_id = $1 AND r.user_id = ar.user_id
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
			DELETE FROM reed_allocations ra
			USING reeds r
			WHERE ra.reed_id = r.id AND ra.holder_user_id = $1 AND r.user_id = $2
			RETURNING ra.reed_id
		),
		counts AS (
			SELECT reed_id, COUNT(*) AS cnt
			FROM deleted
			GROUP BY reed_id
		),
		updated AS (
			UPDATE reeds r
			SET allocation_count = GREATEST(0, r.allocation_count - c.cnt)
			FROM counts c
			WHERE r.id = c.reed_id
			RETURNING r.user_id, c.reed_id
		)
		SELECT user_id, reed_id FROM updated
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

// peerRelaySentinelUserID is the reserved bare userID minted for a
// per-peer sentinel identity representing "peer server X, proxying a
// REQUEST_REED on behalf of one of its users." Underscores never appear
// in a real userID (see crypto.Alphabet), so this can never collide with
// a genuine local or remote user.
const peerRelaySentinelUserID = "__peer_relay__"

// ForeignPendingEvent is an originating-server foreign_pending_events row:
// the mapping from a local pending_events.event_id to the outstanding
// registration on the reed's home server (which peer to call back, and
// what id THEY know this event by). The event's subject (reed_id) lives
// in the normal pending_reed_events row now — reed_identities lets that
// table hold a foreign reed_id directly, so this table only carries what
// pending_reed_events structurally can't.
type ForeignPendingEvent struct {
	EventID      string
	HomeServerID string
	PeerEventID  string
}

// CreateForeignPendingEvent records, on the originating server, that
// eventID's local pending_events row corresponds to peerEventID on
// homeServerID. eventID must already exist in pending_events (FK) —
// created via the normal CreatePendingReedEvent, same as any local event.
func (ds *DBService) CreateForeignPendingEvent(ctx context.Context, eventID, homeServerID, peerEventID string) error {
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO foreign_pending_events (event_id, home_server_id, peer_event_id)
		VALUES ($1, $2, $3)
	`, eventID, homeServerID, peerEventID)
	return err
}

// GetForeignPendingEvent resolves the originating server's own event_id
// to its foreign_pending_events row — used when O needs to notify H that
// delivery was verified and acked (the peer_event_id and home server to
// call), the reverse direction of GetForeignPendingEventByPeerEventID.
func (ds *DBService) GetForeignPendingEvent(ctx context.Context, eventID string) (*ForeignPendingEvent, error) {
	var fpe ForeignPendingEvent
	err := ds.db.QueryRowContext(ctx, `
		SELECT event_id, home_server_id, peer_event_id
		FROM foreign_pending_events
		WHERE event_id = $1
	`, eventID).Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fpe, nil
}

// GetForeignPendingEventByPeerEventID resolves a home server's callback
// (deliver/cancel) back to the originating server's local event. The
// homeServerID filter doubles as an ownership check: only the peer that
// was actually registered as home_server_id for this row may resolve it.
func (ds *DBService) GetForeignPendingEventByPeerEventID(ctx context.Context, peerEventID, homeServerID string) (*ForeignPendingEvent, error) {
	var fpe ForeignPendingEvent
	err := ds.db.QueryRowContext(ctx, `
		SELECT event_id, home_server_id, peer_event_id
		FROM foreign_pending_events
		WHERE peer_event_id = $1 AND home_server_id = $2
	`, peerEventID, homeServerID).Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fpe, nil
}

// GetForeignPendingEventsByRequester lists a requester's outstanding
// cross-server relay requests, for disconnect-cleanup to notify each home
// server before the requester's pending_events rows cascade-delete.
func (ds *DBService) GetForeignPendingEventsByRequester(ctx context.Context, requesterUserID string) ([]ForeignPendingEvent, error) {
	requesterIdentity := identity.IdentityID(requesterUserID)
	rows, err := ds.db.QueryContext(ctx, `
		SELECT fpe.event_id, fpe.home_server_id, fpe.peer_event_id
		FROM foreign_pending_events fpe
		JOIN pending_events pe ON pe.event_id = fpe.event_id
		WHERE pe.requester_user_id = $1
	`, requesterIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ForeignPendingEvent
	for rows.Next() {
		var fpe ForeignPendingEvent
		if err := rows.Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID); err != nil {
			return nil, err
		}
		out = append(out, fpe)
	}
	return out, rows.Err()
}

// EnsurePeerSentinelUser idempotently mints (or reuses) a per-peer
// sentinel identity + online_users row on the home server, satisfying
// pending_events.requester_user_id's FK without representing a genuine
// local session. Mirrors DataService.UpsertRemoteIdentity's insert shape
// in the main package (id/bare_user_id/server_id/verified), and is
// permanently "online" — ON CONFLICT DO NOTHING, not a session refresh.
func (ds *DBService) EnsurePeerSentinelUser(ctx context.Context, peerServerID string) (string, error) {
	sentinelIdentity := identity.CanonicalID(peerServerID, peerRelaySentinelUserID)

	tx, err := ds.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, bare_user_id, server_id, verified)
		VALUES ($1, $2, $3, TRUE)
		ON CONFLICT (id) DO NOTHING
	`, sentinelIdentity, peerRelaySentinelUserID, peerServerID); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO online_users (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING
	`, sentinelIdentity); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return string(sentinelIdentity), nil
}

// UpsertReedIdentity idempotently records that reedID is a well-formed
// id worth tracking, keyed to its own embedded home serverID — the
// "identities layer" for reeds (mirrors identities.id's relationship to
// users.id). This is a low bar, matching what a LOCAL reed already
// clears just by being signed (before anyone verifies/holds its
// content): it does not claim the reed's content has been verified, only
// that this server has legitimate reason to track pending state about
// it (a REQUEST_REED/SUBSCRIBE_PROFILE registration for it exists).
// Content-level trust (e.g. becoming a holder via reed_allocations)
// still gates on the client's own DATA_ACK, same as before. Idempotent;
// safe to call for a reed that already has a row (local reeds get theirs
// at CreateReed time instead, see services.go's insertReedCoreTx).
func (ds *DBService) UpsertReedIdentity(ctx context.Context, reedID string) error {
	_, serverID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		return fmt.Errorf("malformed reed id: %s", reedID)
	}
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, reedID, serverID)
	return err
}

// UpsertRemoteIdentity lazily creates a minimal, unverified identities row
// for a foreign user this server needs to reference (e.g. as a
// profile_subscriptions.viewer_user_id) but has no local users row for.
// Mirrors the root package's DataService.UpsertRemoteIdentity exactly.
func (ds *DBService) UpsertRemoteIdentity(ctx context.Context, canonicalID, remoteServerID string) error {
	bareUserID, _, ok := identity.ParseIdentityID(identity.IdentityID(canonicalID))
	if !ok {
		return fmt.Errorf("malformed identity id: %s", canonicalID)
	}
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO identities (id, bare_user_id, server_id, verified)
		VALUES ($1, $2, $3, FALSE)
		ON CONFLICT (id) DO NOTHING
	`, canonicalID, bareUserID, remoteServerID)
	return err
}

// ForeignRelayRequest is a home-server foreign_relay_requests row: which
// peer+user a sentinel-attributed pending_events row was really
// registered on behalf of.
type ForeignRelayRequest struct {
	EventID            string
	RequestingServerID string
	RequestingUserID   string
}

// CreateForeignRelayRequest records, on the home server, which peer+user
// eventID's sentinel-attributed pending_events row represents.
func (ds *DBService) CreateForeignRelayRequest(ctx context.Context, eventID, requestingServerID, requestingUserID string) error {
	_, err := ds.db.ExecContext(ctx, `
		INSERT INTO foreign_relay_requests (event_id, requesting_server_id, requesting_user_id)
		VALUES ($1, $2, $3)
	`, eventID, requestingServerID, requestingUserID)
	return err
}

// GetForeignRelayRequest returns nil if eventID is an ordinary local
// event (no cross-server registration exists for it).
func (ds *DBService) GetForeignRelayRequest(ctx context.Context, eventID string) (*ForeignRelayRequest, error) {
	var frr ForeignRelayRequest
	err := ds.db.QueryRowContext(ctx, `
		SELECT event_id, requesting_server_id, requesting_user_id
		FROM foreign_relay_requests
		WHERE event_id = $1
	`, eventID).Scan(&frr.EventID, &frr.RequestingServerID, &frr.RequestingUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &frr, nil
}
