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

// LikeCert is both the stored and wire shape of a signed reed-like
// certificate, returned only from POST .../like. Each field was actually
// signed by the liker's key and countersigned by the server; the liker
// is always the authenticated caller.
type LikeCert struct {
	ServerID        string          `json:"serverID"`
	AuthorID        string          `json:"authorID"`
	ReedID          string          `json:"reedID"`
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
	// base_url/connected are federation fields: unset/false on the self
	// row. A federated peer row is written by the RESPONDER before it even
	// attempts the handshake (so there's somewhere to log against from the
	// first moment), then connected flips to TRUE once the initiator's
	// /connect callback confirms success.
	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(16) UNIQUE,
		name VARCHAR(255) PRIMARY KEY,
		self BOOLEAN NOT NULL DEFAULT FALSE,
		signing_key VARCHAR(255),
		identity_backup_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		base_url TEXT,
		connected BOOLEAN NOT NULL DEFAULT FALSE
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

	// identities is the FK target for "a user," local or federated. id is
	// always "{userID}@{serverID}"; verified is a durability flag, not a
	// revocation check — read paths should query verified_identities (below).
	createIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS identities (
		id VARCHAR(255) PRIMARY KEY,
		remote_user_id VARCHAR(255) NOT NULL,
		server_id VARCHAR(16) REFERENCES servers(id),
		public_key_fingerprint VARCHAR(255),
		verified BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		UNIQUE (remote_user_id, server_id)
	);`

	createIdentitiesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_identities_server_id
		ON identities(server_id);
	`

	// users is now a satellite of identities — profile fields only. id IS
	// identities.id: "{userID}@{serverID}" directly, no separate bare column.
	// user_fingerprint is the denormalized active-key hint (updated on key
	// rotation); the signing key lives on user_signatures via user_signature_id.
	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		username VARCHAR(255) UNIQUE,
		role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (role IN ('root', 'admin', 'user')),
		bio TEXT,
		user_fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		user_signature_id INT REFERENCES user_signatures(id),
		server_signature_id INT REFERENCES server_signatures(id),
		invited_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL
	);`

	createUserIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_lower_users_username
		ON users(LOWER(username));
	`

	// verified_identities is the single safe-to-display/safe-to-trust
	// surface. Until federation_established lands, this view is equivalent
	// to "local OR identities.verified" — MUST be tightened once it ships.
	createVerifiedIdentitiesView := `
	CREATE OR REPLACE VIEW verified_identities AS
	SELECT i.*
	FROM identities i
	JOIN servers s ON s.id = i.server_id
	WHERE s.self = TRUE
	   OR i.verified = TRUE;
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
		owner VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		owner VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		allocation_count INT NOT NULL DEFAULT 0,
		like_count INT NOT NULL DEFAULT 0,

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
	// is_blank: the echoing reed carried no commentary (a bare re-share) —
	// same "blank echo" concept as the client's isBlankEcho/
	// resolveBlankEchoChain. The server never stores reed content, so this
	// is captured once at insert time (SignReed has contentBody in scope) —
	// it's what lets SignReed reject a reply/echo aimed at a blank echo
	// instead of the underlying original, without having to store or
	// re-derive content.
	createReedEchoesTable := `
	CREATE TABLE IF NOT EXISTS reed_echoes (
		echoing_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		echoing_reed_id VARCHAR(255) NOT NULL,
		echoed_user_id VARCHAR(255) NOT NULL,
		echoed_reed_id VARCHAR(255) NOT NULL,
		is_blank BOOLEAN NOT NULL DEFAULT FALSE,
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

	// One row per (reed, mentioned user). mentioning_* = reed that contains
	// the @. mentioned_user_id FKs to identities(id), which can hold a
	// provisional remote identity, but only LOCAL mentions are inserted today.
	createReedMentionsTable := `
	CREATE TABLE IF NOT EXISTS reed_mentions (
		mentioning_user_id VARCHAR(255) NOT NULL,
		mentioning_reed_id VARCHAR(255) NOT NULL,
		mentioned_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		mentioned_server_id VARCHAR(255) NOT NULL,

		PRIMARY KEY (mentioning_reed_id, mentioned_server_id, mentioned_user_id),
		FOREIGN KEY (mentioning_user_id, mentioning_reed_id)
			REFERENCES reeds(user_id, id) ON DELETE CASCADE
	);`

	createReedMentionsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_mentions_mentioned
		ON reed_mentions (mentioned_server_id, mentioned_user_id);

	CREATE INDEX IF NOT EXISTS idx_reed_mentions_reed
		ON reed_mentions (mentioning_reed_id);
	`

	// Signed reed-removal certificates. Source of truth for “gone”; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// PK is (user_id, reed_id). user_fingerprint binds the signing key;
	// signatures via FKs.
	createReedRemovalsTable := `
	CREATE TABLE IF NOT EXISTS reed_removals (
		reed_id VARCHAR(255) NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		note VARCHAR(140) NOT NULL DEFAULT '',
		user_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		FOREIGN KEY (user_id, user_fingerprint)
			REFERENCES user_keys(owner, fingerprint)
			ON DELETE CASCADE,
		CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140)
	);`

	// Signed like certificates, one row per currently-liked (liker, reed)
	// pair; unliking hard-deletes the row. liker_fingerprint binds the
	// signing key (same class as reed removals).
	createReedsLikedTable := `
	CREATE TABLE IF NOT EXISTS reeds_liked (
		liker_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		author_user_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,
		liker_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		PRIMARY KEY (liker_user_id, author_user_id, reed_id),
		FOREIGN KEY (author_user_id, reed_id) REFERENCES reeds(user_id, id),
		FOREIGN KEY (liker_user_id, liker_fingerprint)
			REFERENCES user_keys(owner, fingerprint)
			ON DELETE CASCADE
	);`

	createReedsLikedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_liked_liker_created
		ON reeds_liked (liker_user_id, user_signature_id DESC);
	CREATE INDEX IF NOT EXISTS idx_reeds_liked_reed
		ON reeds_liked (author_user_id, reed_id);
	`

	// //////////// //
	//   Ripples    //
	// //////////// //

	createRipplesTable := `
	CREATE TABLE IF NOT EXISTS ripples (
		reed_author_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,
		expires_at TIMESTAMP NOT NULL,

		PRIMARY KEY (reed_author_id, reed_id),
		FOREIGN KEY (reed_author_id, reed_id) REFERENCES reeds(user_id, id)
			ON DELETE CASCADE
	);`

	createRipplesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripples_expires
		ON ripples (expires_at);
	`

	createRippleResponsesTable := `
	CREATE TABLE IF NOT EXISTS ripple_responses (
		id VARCHAR(64) PRIMARY KEY,
		reed_author_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,
		thread_id UUID NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		content VARCHAR(140) NOT NULL,
		replying_to VARCHAR(64) REFERENCES ripple_responses(id) ON DELETE SET NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE,
		posted_at TIMESTAMP NOT NULL,

		user_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		FOREIGN KEY (reed_author_id, reed_id) REFERENCES ripples(reed_author_id, reed_id)
			ON DELETE CASCADE
	);`

	createRippleResponsesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripple_responses_reed_thread_posted
		ON ripple_responses (reed_author_id, reed_id, thread_id, posted_at);
	CREATE INDEX IF NOT EXISTS idx_ripple_responses_thread_posted
		ON ripple_responses (thread_id, posted_at);
	`

	// ////////// //
	//   Social   //
	// ////////// //

	createUserFollowersTable := `
	CREATE TABLE IF NOT EXISTS user_followers (
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		following_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
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
		holder_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE
	);`

	createProfileSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS profile_subscriptions (
		subscription_id VARCHAR(255) PRIMARY KEY,
		viewer_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		author_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createOngoingRecoveriesTable := `
	CREATE TABLE IF NOT EXISTS ongoing_recoveries (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// Follow edges reported during recovery whose target has no identities
	// row yet. following_user_id has no FK — holds unknown targets until
	// claim/peer report drains them into user_following/user_followers.
	createPendingFollowsTable := `
	CREATE TABLE IF NOT EXISTS pending_follows (
		follower_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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
		created_by VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		id VARCHAR(255) NOT NULL,
		token_hash BYTEA NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		claimed_at TIMESTAMPTZ,
		claimed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		revoked_at TIMESTAMPTZ,
		granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (granted_role IN ('admin', 'user')),

		PRIMARY KEY (created_by, id)
	);`

	// federation_invitation covers the whole lifecycle of one federation
	// handshake — there is no separate "attempt" table. status:
	//   new -> accepted        (responder's /connect callback verifies)
	//   new -> canceled        (revoked before anyone redeemed it)
	//   accepted -> approved   (second local admin approves — see 03)
	//   accepted -> rejected   (second local admin rejects — see 03)
	//   approved -> revoked    (an established connection is torn down — see 05)
	// reviewed_by/reviewed_at record whoever performed the terminal action
	// for whichever status it ended up in (cancel, approve, reject, or
	// revoke) — one pair of columns for all of them, since status already
	// says which action reviewed_by refers to; no separate approved_by
	// column duplicating the same fact under a different name.
	// server_id is set once the responder's callback confirms it (FK to
	// servers, not a bare string — base_url/fingerprint live there too,
	// see servers table, so they're not duplicated here).
	createFederationInvitationTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation (
		id VARCHAR(255) PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		secret_hash BYTEA NOT NULL,
		fingerprint VARCHAR(255) NOT NULL REFERENCES public_keys(fingerprint),
		created_by VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		accepted_at TIMESTAMPTZ,
		server_id VARCHAR(16) REFERENCES servers(id),
		status VARCHAR(16) NOT NULL DEFAULT 'new'
			CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
		reviewed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		reviewed_at TIMESTAMPTZ,
		connection_ciphertext TEXT
	);`

	// federation_log: generic append-only log line, not itself tied to an
	// invitation or server — two junction tables link a line to whichever
	// it's about. Handshake steps happen asynchronously across two
	// servers (connect callbacks, outbound POSTs that may fail or time
	// out), so this is how an admin sees what actually happened instead
	// of a link silently never progressing.
	createFederationLogTable := `
	CREATE TABLE IF NOT EXISTS federation_log (
		id VARCHAR(255) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		level VARCHAR(16) NOT NULL
			CHECK (level IN ('info', 'error')),
		message TEXT NOT NULL
	);`

	// federation_invitation_log: the INITIATOR logs against its
	// invitation from the moment a connect callback arrives (known
	// immediately, regardless of accept/reject outcome) — the
	// invitation's server_id isn't set until AFTER acceptance succeeds,
	// so pre-acceptance rejections (bad secret, wrong status, bad
	// signature) have nothing else to log against yet.
	createFederationInvitationLogTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation_log (
		invitation_id VARCHAR(255) NOT NULL REFERENCES federation_invitation(id) ON DELETE CASCADE,
		log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,

		PRIMARY KEY (invitation_id, log_id)
	);`

	// federation_server_log: server-scoped log lines. The RESPONDER always
	// uses this (it never has a local invitation row to log against) from
	// the moment it records the peer server row, before attempting the
	// handshake. The initiator also logs here for server-level events
	// after a server_id exists (i.e. post-acceptance).
	createFederationServerLogTable := `
	CREATE TABLE IF NOT EXISTS federation_server_log (
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,

		PRIMARY KEY (server_id, log_id)
	);`

	// Device binding — append-only history; exactly one active row per user.
	createUserDevicesTable := `
	CREATE TABLE IF NOT EXISTS user_devices (
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
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

		createIdentitiesTable,
		createIdentitiesIndexes,

		createUsersTable,
		createUserIndexes,

		createVerifiedIdentitiesView,

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

		createReedMentionsTable,
		createReedMentionsIndexes,

		createReedRemovalsTable,
		createAccountRemovalsTable,

		createReedsLikedTable,
		createReedsLikedIndexes,

		createRipplesTable,
		createRipplesIndexes,

		createRippleResponsesTable,
		createRippleResponsesIndexes,

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
		createFederationLogTable,
		createFederationInvitationLogTable,
		createFederationServerLogTable,
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
