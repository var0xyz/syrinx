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

// User is the wire shape of an identity record.
//
// Layout: user-authored fields live at the root; the server-authored
// countersignature and its metadata live under `server`. `signature` (at
// the root) is the user's detached PGP signature over the user identity
// payload (see identity.go). `SignatureFingerprint` identifies the user
// key that produced `Signature` — it is self-describing per record, not
// a pointer to the user's "current" key.
//
// `ActiveKeyFingerprint` is a server-provided convenience field
// carrying the user's currently-active key fingerprint at response
// time. It is deliberately **outside** the signed payload: the identity
// record is a frozen artifact from the moment it was minted, whereas
// the "current" key can change without a new identity record (a
// rotation may occur between profile updates). Clients use
// `ActiveKeyFingerprint` as a hint to decide whether to re-fetch the
// record's signing key (if
// `SignatureFingerprint != ActiveKeyFingerprint`, the signer has been
// rotated and the client should pull the fresh key to learn revocation
// state and walk the successor chain to the active one).
//
// TODO(signed-fields): the response now mixes three trust tiers —
// fields signed by the user, fields signed by the server, and unsigned
// server-provided hints like ActiveKeyFingerprint. This is currently
// implicit and easy to get wrong. We should rework the wire shape so
// each signature is accompanied by an explicit manifest of which fields
// it covers, so a verifier can programmatically distinguish "signed by
// X", "signed by Y", and "not signed" without out-of-band knowledge.
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatarURL"`
	Bio       string    `json:"bio"`
	CreatedAt time.Time `json:"memberSince"`
	HasReeds  bool      `json:"hasReeds"`

	// SignatureFingerprint is the fingerprint of the user key that
	// produced `Signature`. Self-describing per record: it identifies
	// which key to verify with, not which key is currently active. Note
	// that the canonical signed payload (see identity.go) still spells
	// this header `fingerprint`; the JSON field is a wire-only alias
	// and does not affect signature verification.
	SignatureFingerprint string `json:"signatureFingerprint"`

	// ActiveKeyFingerprint is a server-provided hint carrying the
	// user's currently-active key fingerprint at response time. See
	// the struct-level doc comment for the trust-tier caveat.
	ActiveKeyFingerprint string    `json:"activeKeyFingerprint"`
	UserSignatureB64     string    `json:"signature"`
	Server               Signature `json:"server"`
}

// Signature is the server's countersignature over a signed resource
// (identity, public key, reed, …): which server key signed it, when,
// and the signature itself.
type Signature struct {
	ServerID    string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Algorithm   string    `json:"algorithm"`
	Armor       string    `json:"signature"`
	SignedAt    time.Time `json:"timestamp"`
}

// KeyPredecessor is the rotation handoff proof bundled on keys uploaded
// via AddPublicKey: the revoked predecessor's detached signature over
// this key's armor, and which key produced it.
type KeyPredecessor struct {
	Fingerprint string `json:"fingerprint"`
	Signature   string `json:"signature"`
}

// Key is the wire shape of a distributed user public key. `Server` is
// required: the countersignature over (userID, fingerprint, armor).
// `Revoked` is computed on read from user_key_revocations — never stored
// on user_keys.
//
// `Predecessor` is set for rotation keys only; signup keys return null.
type Key struct {
	Fingerprint string          `json:"fingerprint"`
	UserID      string          `json:"userID"`
	Armor       string          `json:"armor"`
	CreatedAt   time.Time       `json:"createdAt"`
	Revoked     bool            `json:"revoked"`
	Predecessor *KeyPredecessor `json:"predecessor"`
	Server      Signature       `json:"server"`
}

// KeyRevocation is the wire shape of a signed revocation attestation.
// The user signature covers (userID, fingerprint, reason); the server
// countersignature binds that user attestation and supplies the
// authoritative revoke time as server.timestamp.
//
// Successor is bookkeeping written later by AddPublicKey when the
// replacement key is uploaded. It is returned on GET when present but
// is not covered by either signature — it is unknown at revoke time.
type KeyRevocation struct {
	Fingerprint string    `json:"fingerprint"`
	UserID      string    `json:"userID"`
	Reason      string    `json:"reason"`
	Successor   *string   `json:"successor"`
	Signature   string    `json:"signature"`
	Server      Signature `json:"server"`
}

type Reed struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userID"`
	Fingerprint string    `json:"fingerprint"`
	Timestamp   time.Time `json:"timestamp"`
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

	// Existing deployments created servers without identity_backup_at.
	alterServersIdentityBackupAt := `
	ALTER TABLE servers
		ADD COLUMN IF NOT EXISTS identity_backup_at TIMESTAMP;
	`

	// Signed identity record columns. All four are populated together
	// at signup and re-populated together whenever a fresh identity
	// record is minted (e.g. profile updates). NOT NULL: every user
	// row is a countersigned identity record.
	//
	// - user_signature: base64 of the user's armored PGP detached
	//   signature over the user identity payload.
	// - server_signature: base64 of the server's armored PGP detached
	//   signature over the server identity payload.
	// - server_signed_at: timestamp inside the server-signed payload.
	//   Used later for monotonic newest-wins during recovery.
	// - server_fingerprint: fingerprint of the server key that produced
	//   server_signature. Stored explicitly because the server's
	//   active signing key can rotate; verifiers need to know which
	//   historical key to look up.
	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR(255) PRIMARY KEY,
		username VARCHAR(255) UNIQUE NOT NULL,
		avatar_url VARCHAR(255),
		bio TEXT,
		fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		user_signature TEXT NOT NULL,
		server_signature TEXT NOT NULL,
		server_signed_at TIMESTAMP NOT NULL,
		server_fingerprint VARCHAR(255) NOT NULL
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

	// Client-managed public keys. predecessor_signature and
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
		server_signature TEXT NOT NULL,
		server_fingerprint VARCHAR(255) NOT NULL,
		server_signed_at TIMESTAMP NOT NULL,
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
	// key is revoked. Revoke time is server_signed_at (wire:
	// server.timestamp), not a separate column. user_signature and
	// server_signature are base64 armored PGP detached signatures over
	// the canonical revocation payloads (see identity.go).
	//
	// successor is written when the replacement key is uploaded via
	// AddPublicKey, not at revocation time — the client revokes first
	// and adds the new key second, so at RevokeKey we do not yet know
	// the successor.
	createUserKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS user_key_revocations (
		fingerprint VARCHAR(255),
		owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		reason TEXT,
		user_signature TEXT NOT NULL,
		server_signature TEXT NOT NULL,
		server_fingerprint VARCHAR(255) NOT NULL,
		server_signed_at TIMESTAMP NOT NULL,
		successor VARCHAR(255) REFERENCES user_keys(fingerprint),

		PRIMARY KEY (fingerprint, owner),
		FOREIGN KEY (fingerprint, owner)
			REFERENCES user_keys(fingerprint, owner)
			ON DELETE CASCADE
	);`

	// We only need one index on `owner` here because `fingerprint` is covered
	// by being the first field in the `PRIMARY KEY` clause.
	createUserKeyRevocationsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_key_revocations_owner
		ON user_key_revocations(owner);
	`

	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) UNIQUE NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,

		PRIMARY KEY (id, user_id)
	);`

	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	`

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

	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL REFERENCES reeds(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (reed_id, user_id)
	);`

	// We only need one index on `user_id` here because `reed_id` is covered by
	// being the first field in the `PRIMARY KEY` clause.
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_user_id
		ON reed_allocations(user_id);
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

	createPendingReedRequestsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_reed_requests (
		event_id VARCHAR(255) PRIMARY KEY REFERENCES pending_events(event_id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL
	);`

	createPendingReedRequestsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pending_reed_requests_reed_id
		ON pending_reed_requests(reed_id);
	`

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

	queries := []string{
		// Servers
		createServersTable,
		alterServersIdentityBackupAt,

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

		// Social
		createUserFollowersTable,
		createUserFollowerIndexes,

		createUserFollowingTable,
		createUserFollowingIndexes,

		// Realtime
		createOnlineUsersTable,

		createBroadcastSubscriptionsTable,
		createBroadcastSubscriptionIndexes,

		createReedAllocationsTable,
		createReedAllocationIndexes,

		createProfileSubscriptionsTable,
		createProfileSubscriptionsIndex,

		createPendingEventsTable,
		createPendingEventsIndexes,

		createPendingReedRequestsTable,
		createPendingReedRequestsIndexes,

		// Recovery
		createUnclaimedAccountsTable,

		createOngoingRecoveriesTable,

		createPendingFollowsTable,
		createPendingFollowsIndexes,
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
