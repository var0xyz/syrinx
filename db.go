package main

import (
	"database/sql"
	"log"
	"time"
)

// /////// //
//   API   //
// /////// //

type Server struct {
	Id         string    `json:"id"`
	Name       string    `json:"name"`
	Self       bool      `json:"self"`
	SigningKey Key       `json:"signingKey"`
	CreatedAt  time.Time `json:"createdAt"`
}

// User is the wire shape of a signed identity record (GET /users/{id}/profile).
//
// Layout: user-authored fields live at the root; attestations nest under
// `userSignature` and `serverSignature`.
//
// Mutable / unsigned hints (hasReeds, follow counts, activeKeyFingerprint)
// live on UserInfo (GET /users/{id}/info), not here.
type User struct {
	ID              string          `json:"id"`
	Username        string          `json:"username"`
	Role            string          `json:"role"`
	Bio             string          `json:"bio"`
	CreatedAt       time.Time       `json:"memberSince"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
	InvitedBy       *InvitedBy      `json:"invitedBy"`
}

// UserInfo is the unsigned, frequently changing view of a user
// (GET /users/{id}/info). ProfileTimestamp matches the user's current
// profile serverSignature.timestamp so clients can invalidate a cached
// signed profile.
type UserInfo struct {
	ID                   string    `json:"id"`
	HasReeds             bool      `json:"hasReeds"`
	FollowersCount       int       `json:"followersCount"`
	FollowingCount       int       `json:"followingCount"`
	ActiveKeyFingerprint string    `json:"activeKeyFingerprint"`
	ProfileTimestamp     time.Time `json:"profileTimestamp"`
}

// InvitedBy is the durable inviter binding nested on User wire when set.
type InvitedBy struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// UserSignature is the nested user attestation wire block.
type UserSignature struct {
	Fingerprint string `json:"fingerprint"`
	Armor       string `json:"armor"`
}

// ServerSignature is the nested server countersignature wire block
// (identity, public key, reed, …): which server key signed it, when,
// and the signature itself.
type ServerSignature struct {
	ServerID    string    `json:"serverID"`
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"armor"`
	SignedAt    time.Time `json:"timestamp"`
}

// KeyPredecessor is the rotation handoff proof bundled on keys uploaded
// via AddPublicKey: the revoked predecessor's detached signature over
// this key's armor, and which key produced it.
type KeyPredecessor struct {
	Fingerprint string `json:"fingerprint"`
	Signature   string `json:"signature"`
}

// Key is the wire shape of a distributed user public key.
// `ServerSignature` is required: the countersignature over
// (userID, fingerprint, armor). `Revoked` is computed on read from
// user_key_revocations — never stored on user_keys.
//
// `Predecessor` is set for rotation keys only; signup keys return null.
type Key struct {
	Fingerprint     string          `json:"fingerprint"`
	UserID          string          `json:"userID"`
	Armor           string          `json:"armor"`
	CreatedAt       time.Time       `json:"createdAt"`
	Revoked         bool            `json:"revoked"`
	Predecessor     *KeyPredecessor `json:"predecessor"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// KeyRevocation is the wire shape of a signed revocation attestation.
// The user signature covers (userID, fingerprint, reason); the server
// countersignature binds that user attestation and supplies the
// authoritative revoke time as serverSignature.timestamp.
//
// Successor is bookkeeping written later by AddPublicKey when the
// replacement key is uploaded. It is returned on GET when present but
// is not covered by either signature — it is unknown at revoke time.
type KeyRevocation struct {
	Fingerprint     string          `json:"fingerprint"`
	UserID          string          `json:"userID"`
	Reason          string          `json:"reason"`
	Successor       *string         `json:"successor"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

type Reed struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userID"`
	Fingerprint string    `json:"fingerprint"`
	Timestamp   time.Time `json:"timestamp"`
}

// ReedRemoval is the wire shape of a signed reed-removal certificate
// (JSON `type: "reed"`).
type ReedRemoval struct {
	Type            string          `json:"type"`
	ServerID        string          `json:"serverID"`
	UserID          string          `json:"userID"`
	ReedID          string          `json:"reedID"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// AccountRemoval is the wire shape of a signed account-removal certificate
// (JSON `type: "account"`).
type AccountRemoval struct {
	Type            string          `json:"type"`
	ServerID        string          `json:"serverID"`
	UserID          string          `json:"userID"`
	Note            string          `json:"note"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// /////// //
//   P2P   //
// /////// //

func InitDB(db *sql.DB) error {
	log.Println("Initializing database schema...")

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // This will be ignored if tx.Commit() is called

	// /////// //
	//   API   //
	// /////// //
	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(16) UNIQUE,
		name VARCHAR(255) PRIMARY KEY,
		self BOOLEAN NOT NULL DEFAULT FALSE,
		signing_key VARCHAR(255),
		identity_backup_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// Normalized attestation rows (signatures proposal 01). Entities will
	// FK here in later migrate steps; fingerprint is not FK'd to key
	// tables (historical / rotated keys).
	createUserSignaturesTable := `
	CREATE TABLE IF NOT EXISTS user_signatures (
		id SERIAL PRIMARY KEY,
		fingerprint VARCHAR(255) NOT NULL,
		signature TEXT NOT NULL
	);`

	createServerSignaturesTable := `
	CREATE TABLE IF NOT EXISTS server_signatures (
		id SERIAL PRIMARY KEY,
		fingerprint VARCHAR(255) NOT NULL,
		signature TEXT NOT NULL,
		signed_at TIMESTAMP NOT NULL
	);`

	// users holds profile fields plus FKs to normalized attestation
	// rows. user_fingerprint is the denormalized active-key hint (updated
	// on key rotation); the signing key for the current identity record
	// lives on user_signatures via user_signature_id.
	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR(255) PRIMARY KEY,
		username VARCHAR(255) UNIQUE NOT NULL,
		role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (role IN ('root', 'admin', 'user')),
		bio TEXT,
		user_fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		invited_by VARCHAR(255) REFERENCES users(id)
	);`

	createUserIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_lower_users_username
		ON users(LOWER(username));
	`

	// Server-owned private keys
	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		revoked_at TIMESTAMP,
		revoke_reason TEXT
	);`

	// Server-owned public keys
	createPublicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// Client-managed public keys. Server countersignature is via
	// server_signature_id. predecessor_signature and
	// predecessor_fingerprint are set together for rotation keys only
	// (AddPublicKey): the old key's detached signature over this row's
	// armor, and which key produced it. Signup keys leave both NULL.
	createUserKeysTable := `
	CREATE TABLE IF NOT EXISTS user_keys (
		fingerprint VARCHAR(255) UNIQUE NOT NULL,
		owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP,
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		predecessor_signature TEXT,
		predecessor_fingerprint VARCHAR(255) REFERENCES user_keys(fingerprint),

		PRIMARY KEY (owner, fingerprint)
	);`

	// We only need one index on `fingerprint` here because `owner` is covered
	// by being the first field in the `PRIMARY KEY` clause.
	createUserKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_keys_fingerprint
		ON user_keys(fingerprint);
	`

	// Revocation attestation for a user key. A row's existence means the
	// key is revoked. user_fingerprint identifies which key was revoked
	// (PK + FK to user_keys). Signatures live in user_signatures /
	// server_signatures; revoke time is server_signatures.signed_at.
	//
	// successor is written when the replacement key is uploaded via
	// AddPublicKey, not at revocation time — the client revokes first
	// and adds the new key second, so at RevokeKey we do not yet know
	// the successor.
	createUserKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS user_key_revocations (
		user_fingerprint VARCHAR(255) NOT NULL,
		owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		reason TEXT,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		successor VARCHAR(255) REFERENCES user_keys(fingerprint),

		PRIMARY KEY (owner, user_fingerprint),
		FOREIGN KEY (owner, user_fingerprint)
			REFERENCES user_keys(owner, fingerprint)
			ON DELETE CASCADE
	);`

	// We only need one index on `user_fingerprint` here because `owner` is
	// covered by being the first field in the `PRIMARY KEY` clause.
	createUserKeyRevocationsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_key_revocations_user_fingerprint
		ON user_key_revocations(user_fingerprint);
	`

	// Tip reed metadata. private_key_fingerprint is the server key used
	// for the countersignature. user_signature_id / server_signature_id
	// store the attestations so SignReed retries can return the same
	// countersignature (idempotent).
	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		allocation_count INT NOT NULL DEFAULT 0,

		PRIMARY KEY (user_id, id)
	);`

	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_id
		ON reeds(id);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	`

	// echoing_* is the reed that does the echo
	// echoed_* is the reed it points at.
	createReedEchoesTable := `
	CREATE TABLE IF NOT EXISTS reed_echoes (
		echoing_user_id VARCHAR(255) NOT NULL REFERENCES users(id),
		echoing_reed_id VARCHAR(255) NOT NULL,
		echoed_user_id VARCHAR(255) NOT NULL,
		echoed_reed_id VARCHAR(255) NOT NULL,
		signed_at TIMESTAMP NOT NULL,

		PRIMARY KEY (echoing_user_id, echoing_reed_id)
	);`

	createReedEchoesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_echoes_echoed_signed
		ON reed_echoes (echoed_user_id, echoed_reed_id, signed_at);
	`

	// id is the root reed ref (user@server/reed); one row per thread (created on first reply).
	createReedRepliesTable := `
	CREATE TABLE IF NOT EXISTS reed_replies (
		thread_id VARCHAR(255) NOT NULL,
		user_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL UNIQUE,
		parent_user_id VARCHAR(255) NOT NULL,
		parent_reed_id VARCHAR(255) NOT NULL,
		timestamp TIMESTAMP NOT NULL,

		PRIMARY KEY (user_id, reed_id),
		FOREIGN KEY (user_id, reed_id) REFERENCES reeds(user_id, id),
		FOREIGN KEY (parent_user_id, parent_reed_id) REFERENCES reeds(user_id, id)
	);`

	createReedRepliesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_timestamp
		ON reed_replies (parent_user_id, parent_reed_id, timestamp);

	CREATE INDEX IF NOT EXISTS idx_reed_replies_thread
		ON reed_replies (thread_id, timestamp);
	`

	// Signed reed-removal certificates. Source of truth for “gone”; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// PK is (user_id, reed_id). user_fingerprint binds the signing key;
	// signatures via FKs.
	createReedRemovalsTable := `
	CREATE TABLE IF NOT EXISTS reed_removals (
		reed_id VARCHAR(255) NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id),
		user_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		PRIMARY KEY (user_id, reed_id),
		FOREIGN KEY (user_id, user_fingerprint)
			REFERENCES user_keys(owner, fingerprint)
			ON DELETE CASCADE
	);`

	// Signed account-removal certificates. One cert per user; public keys
	// remain. user_fingerprint binds the signing key (same class as reed
	// removals). note ≤140 enforced by CHECK + API.
	createAccountRemovalsTable := `
	CREATE TABLE IF NOT EXISTS account_removals (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id),
		note VARCHAR(140) NOT NULL DEFAULT '',
		user_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		FOREIGN KEY (user_id, user_fingerprint)
			REFERENCES user_keys(owner, fingerprint)
			ON DELETE CASCADE,
		CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140)
	);`

	// ////////// //
	//   Social   //
	// ////////// //

	createUserFollowersTable := `
	CREATE TABLE IF NOT EXISTS user_followers (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		follower_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, follower_user_id)
	);`

	// We only need one index on `follower_user_id` here because `user_id` is
	// covered by being the first field in the `PRIMARY KEY` clause.
	createUserFollowerIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_followers_follower_user_id
		ON user_followers(follower_user_id);
	`

	createUserFollowingTable := `
	CREATE TABLE IF NOT EXISTS user_following (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		following_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, following_user_id)
	);`

	// We only need one index on `following_user_id` here because `user_id` is
	// covered by being the first field in the `PRIMARY KEY` clause.
	createUserFollowingIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_following_following_user_id
		ON user_following(following_user_id);
	`

	// //////////// //
	//   Realtime   //
	// //////////// //

	createOnlineUsersTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS online_users (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		sync_request_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createBroadcastSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS broadcast_subscriptions (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES online_users(user_id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
		last_delivery TIMESTAMP
	);`

	createBroadcastSubscriptionIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_broadcast_subscriptions_user_id
		ON broadcast_subscriptions(user_id);
	`

	// holder_user_id is who holds the reed; author_user_id + reed_id FK to reeds.
	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL,
		holder_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		author_user_id VARCHAR(255) NOT NULL,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (holder_user_id, author_user_id, reed_id),
		FOREIGN KEY (author_user_id, reed_id)
			REFERENCES reeds(user_id, id) ON DELETE CASCADE
	);`

	// Composite lookups by reed use author_user_id + reed_id; holder is in the PK.
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_reed
		ON reed_allocations(author_user_id, reed_id);
	`

	// tags are normalized hashtag names extracted at SignReed for pipe
	// fanout at PUBLISH_READY (pipes 01). Empty until claim deletes the row.
	createPendingFanoutTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_fanout (
		user_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,
		tags    TEXT[] NOT NULL DEFAULT '{}',

		PRIMARY KEY (user_id, reed_id),
		FOREIGN KEY (user_id, reed_id)
			REFERENCES reeds(user_id, id) ON DELETE CASCADE
	);`

	createNetworkStatsTable := `
	CREATE TABLE IF NOT EXISTS network_stats (
		id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
		active_users INT NOT NULL DEFAULT 0
	);`

	seedNetworkStats := `
	INSERT INTO network_stats (id, active_users) VALUES (TRUE, 0)
	ON CONFLICT (id) DO NOTHING;
	`

	createPendingEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_events (
		event_id VARCHAR(255) PRIMARY KEY,
		request_id VARCHAR(255) NOT NULL,
		requester_user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
		event_name VARCHAR(255) NOT NULL,
		subscription_id VARCHAR(255) REFERENCES profile_subscriptions(subscription_id) ON DELETE CASCADE,
		dispatched_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createPendingEventsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pending_events_requester_user_id
		ON pending_events(requester_user_id);
	CREATE INDEX IF NOT EXISTS idx_pending_events_subscription_id
		ON pending_events(subscription_id);
	`

	createPendingReedEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_reed_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,

		FOREIGN KEY (user_id, reed_id)
			REFERENCES reeds(user_id, id) ON DELETE CASCADE
	);`

	createPendingReedEventsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pending_reed_events_reed_id
		ON pending_reed_events(reed_id);
	`

	createPendingAccountEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_account_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id)
	);`

	createProfileSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS profile_subscriptions (
		subscription_id VARCHAR(255) PRIMARY KEY,
		viewer_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		author_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createProfileSubscriptionsIndex := `
	CREATE INDEX IF NOT EXISTS idx_profile_subscriptions_viewer
		ON profile_subscriptions(viewer_user_id);
	CREATE INDEX IF NOT EXISTS idx_profile_subscriptions_author
		ON profile_subscriptions(author_user_id);
	`

	// //////////// //
	//   Recovery   //
	// //////////// //

	createUnclaimedAccountsTable := `
	CREATE TABLE IF NOT EXISTS unclaimed_accounts (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createOngoingRecoveriesTable := `
	CREATE TABLE IF NOT EXISTS ongoing_recoveries (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// Follow edges reported during recovery whose target is not yet in users.
	// following_user_id has no FK — the whole point is to hold unknown targets
	// until claim / peer report drains them into user_following / user_followers.
	createPendingFollowsTable := `
	CREATE TABLE IF NOT EXISTS pending_follows (
		follower_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		following_user_id VARCHAR(255) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (follower_user_id, following_user_id)
	);`

	createPendingFollowsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pending_follows_following_user_id
		ON pending_follows(following_user_id);
	`

	// Invites — operational redeem state. PK is (created_by, id) because
	// clients mint ids; scoping to the issuer prevents cross-user collisions.
	createInvitesTable := `
	CREATE TABLE IF NOT EXISTS invites (
		created_by VARCHAR(255) NOT NULL REFERENCES users(id),
		id VARCHAR(255) NOT NULL,
		token_hash BYTEA NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		claimed_at TIMESTAMPTZ,
		claimed_by VARCHAR(255) REFERENCES users(id),
		revoked_at TIMESTAMPTZ,
		granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (granted_role IN ('admin', 'user')),

		PRIMARY KEY (created_by, id)
	);`

	createFederationInvitationTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation (
		id VARCHAR(255) PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		secret_hash BYTEA NOT NULL,
		remote_fingerprint VARCHAR(255) NOT NULL,
		status VARCHAR(16) NOT NULL DEFAULT 'new'
			CHECK (status IN ('new', 'accepted', 'approved', 'revoked')),
		created_by VARCHAR(255) NOT NULL REFERENCES users(id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		accepted_at TIMESTAMPTZ,
		approved_at TIMESTAMPTZ,
		reviewed_by VARCHAR(255) REFERENCES users(id),
		reviewed_at TIMESTAMPTZ,
		connection_ciphertext TEXT
	);`

	// Device binding — append-only history; exactly one active row per user.
	createUserDevicesTable := `
	CREATE TABLE IF NOT EXISTS user_devices (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		device_id TEXT NOT NULL,
		linked_at TIMESTAMPTZ NOT NULL,
		revoked_at TIMESTAMPTZ NULL,

		PRIMARY KEY (user_id, device_id, linked_at)
	);`

	createUserDevicesIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS user_devices_one_active_per_user
		ON user_devices (user_id) WHERE revoked_at IS NULL;
	`

	queries := []string{
		createServersTable,

		createUserSignaturesTable,
		createServerSignaturesTable,

		createUsersTable,
		createUserIndexes,

		createPrivateKeysTable,
		createPublicKeysTable,

		createUserKeysTable,
		createUserKeyIndexes,

		createUserKeyRevocationsTable,
		createUserKeyRevocationsIndexes,

		createReedsTable,
		createReedIndexes,

		createReedEchoesTable,
		createReedEchoesIndexes,

		createReedRepliesTable,
		createReedRepliesIndexes,

		createReedRemovalsTable,
		createAccountRemovalsTable,

		createUserDevicesTable,
		createUserDevicesIndexes,

		// Social
		createUserFollowersTable,
		createUserFollowerIndexes,

		createUserFollowingTable,
		createUserFollowingIndexes,

		createInvitesTable,

		// Realtime
		createOnlineUsersTable,

		createBroadcastSubscriptionsTable,
		createBroadcastSubscriptionIndexes,

		createReedAllocationsTable,
		createReedAllocationIndexes,

		createPendingFanoutTable,

		createNetworkStatsTable,
		seedNetworkStats,

		createProfileSubscriptionsTable,
		createProfileSubscriptionsIndex,

		createPendingEventsTable,
		createPendingEventsIndexes,

		createPendingReedEventsTable,
		createPendingReedEventsIndexes,

		createPendingAccountEventsTable,

		// Recovery
		createUnclaimedAccountsTable,

		createOngoingRecoveriesTable,

		createPendingFollowsTable,
		createPendingFollowsIndexes,

		// Federation
		createFederationInvitationTable,
	}

	for i, query := range queries {
		log.Printf("Executing query %d/%d", i+1, len(queries))
		if _, err := tx.Exec(query); err != nil {
			log.Printf("Error executing query %d: %v", i+1, err)
			log.Println(query)
			return err
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return err
	}

	log.Println("Database schema initialized successfully!")
	return nil
}
