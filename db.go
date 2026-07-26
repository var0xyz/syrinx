package main

import (
	"database/sql"
	"encoding/json"
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
// Layout: user-authored fields live at the root; attestations nest under
// `userSignature` and `serverSignature`.
//
// `ActiveKeyFingerprint` is a server-provided convenience field
// carrying the user's currently-active key fingerprint at response
// time. It is deliberately **outside** both signature blocks: the
// identity record is a frozen artifact from the moment it was minted,
// whereas the "current" key can change without a new identity record (a
// rotation may occur between profile updates). Clients use
// `ActiveKeyFingerprint` as a hint to decide whether to re-fetch the
// record's signing key (if
// `UserSignature.Fingerprint != ActiveKeyFingerprint`, the signer has
// been rotated and the client should pull the fresh key to learn
// revocation state and walk the successor chain to the active one).
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatarURL"`
	Bio       string    `json:"bio"`
	CreatedAt time.Time `json:"memberSince"`
	HasReeds  bool      `json:"hasReeds"`

	// ActiveKeyFingerprint is a server-provided hint carrying the
	// user's currently-active key fingerprint at response time. See
	// the struct-level doc comment for the trust-tier caveat.
	ActiveKeyFingerprint string          `json:"activeKeyFingerprint"`
	UserSignature        UserSignature   `json:"userSignature"`
	ServerSignature      ServerSignature `json:"serverSignature"`
	InvitedBy            *InvitedBy      `json:"invitedBy"`
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

// MarshalJSON always emits timestamp as UTC whole-second RFC3339 with a
// trailing Z — the same form used in countersignature headers — so clients
// never see a local offset from TIMESTAMP WITHOUT TIME ZONE round-trips.
func (s ServerSignature) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ServerID    string `json:"serverID"`
		Fingerprint string `json:"fingerprint"`
		Armor       string `json:"armor"`
		Timestamp   string `json:"timestamp"`
	}{
		ServerID:    s.ServerID,
		Fingerprint: s.Fingerprint,
		Armor:       s.Armor,
		Timestamp:   s.SignedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
	})
}

func (s *ServerSignature) UnmarshalJSON(data []byte) error {
	var raw struct {
		ServerID    string `json:"serverID"`
		Fingerprint string `json:"fingerprint"`
		Armor       string `json:"armor"`
		Timestamp   string `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.ServerID = raw.ServerID
	s.Fingerprint = raw.Fingerprint
	s.Armor = raw.Armor
	if raw.Timestamp == "" {
		s.SignedAt = time.Time{}
		return nil
	}
	ts, err := time.Parse(time.RFC3339, raw.Timestamp)
	if err != nil {
		ts, err = time.Parse(time.RFC3339Nano, raw.Timestamp)
		if err != nil {
			return err
		}
	}
	s.SignedAt = ts.UTC().Truncate(time.Second)
	return nil
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
		avatar_url VARCHAR(255),
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

	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) UNIQUE NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,

		PRIMARY KEY (user_id, id)
	);`

	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_id
		ON reeds(id);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	`

	// Signed reed-removal certificates. Source of truth for “gone”; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// PK is (user_id, reed_id); reed_id is also UNIQUE for reed-only lookups.
	// user_fingerprint binds the signing key; signatures via FKs.
	createReedRemovalsTable := `
	CREATE TABLE IF NOT EXISTS reed_removals (
		reed_id VARCHAR(255) UNIQUE NOT NULL,
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

	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL REFERENCES reeds(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, reed_id)
	);`

	// We only need one index on `reed_id` here because `user_id` is covered by
	// being the first field in the `PRIMARY KEY` clause.
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_reed_id
		ON reed_allocations(reed_id);
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

		PRIMARY KEY (created_by, id)
	);`

	queries := []string{
		// Servers
		createServersTable,

		// Shared attestation tables (before entity FKs land)
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

		createReedRemovalsTable,
		createAccountRemovalsTable,

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

		// Invites
		createInvitesTable,
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
