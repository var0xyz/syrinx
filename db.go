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
// User-authored fields live at the root; attestations nest under
// `userSignature` and `serverSignature`.
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
// profile serverSignature.timestamp so clients can invalidate a cached profile.
type UserInfo struct {
	ID               string    `json:"id"`
	HasReeds         bool      `json:"hasReeds"`
	FollowersCount   int       `json:"followersCount"`
	FollowingCount   int       `json:"followingCount"`
	ActiveKeyID      string    `json:"activeKeyID"`
	ProfileTimestamp time.Time `json:"profileTimestamp"`
}

// InvitedBy is the durable inviter binding nested on User wire when set.
type InvitedBy struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// UserSignature is the nested user attestation wire block.
type UserSignature struct {
	ID    string `json:"id"`
	Armor string `json:"armor"`
}

// ServerSignature is the nested server countersignature wire block
// (identity, public key, reed, …): which server key signed it, when,
// and the signature itself.
type ServerSignature struct {
	ID       string    `json:"id"`
	Armor    string    `json:"armor"`
	SignedAt time.Time `json:"timestamp"`
}

// KeyPredecessor identifies the key this one replaced (rotation only).
// The predecessor's detached signature over this key's armor lives on
// the predecessor's own KeyRevocation.SuccessorSignature instead.
type KeyPredecessor struct {
	ID string `json:"id"`
}

// Key is the wire shape of a public key — a local user's or this server's
// own (peer servers' keys are never stored; foreign ids proxy live instead).
// Revoked is computed on read; Predecessor is set for rotation keys only.
type Key struct {
	ID              string          `json:"id"`
	UserID          string          `json:"userID"`
	Armor           string          `json:"armor"`
	CreatedAt       time.Time       `json:"createdAt"`
	Revoked         bool            `json:"revoked"`
	Predecessor     *KeyPredecessor `json:"predecessor"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// ServerSigningKey is the server's own active signing key, held in memory
// for countersign operations — never serialized as wire JSON. Armor is the
// DECRYPTED PRIVATE key armor, never exposed over the wire.
type ServerSigningKey struct {
	Fingerprint string
	Armor       string
	CreatedAt   time.Time
}

// KeyRevocation is the wire shape of a signed revocation attestation.
// The user signature covers (userID, id, reason); the server countersignature
// supplies the revoke time. Successor fields are nil until a replacement key is uploaded.
type KeyRevocation struct {
	ID                 string          `json:"id"`
	UserID             string          `json:"userID"`
	Reason             string          `json:"reason"`
	Successor          *string         `json:"successor"`
	SuccessorSignature *string         `json:"successorSignature"`
	UserSignature      UserSignature   `json:"userSignature"`
	ServerSignature    ServerSignature `json:"serverSignature"`
}

type Reed struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userID"`
	Timestamp time.Time `json:"timestamp"`
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
// certificate, returned only from POST .../like. The liker is always
// the authenticated caller.
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
	// base_url/connected/fingerprint/revoked are federation fields, unset on
	// the self row. fingerprint is the peer's pinned signing key fingerprint —
	// its armor is NEVER stored locally; every key/profile fetch proxies live.
	// signing_key is a soft reference to public_keys(id) (not FK'd — servers
	// is created before public_keys to satisfy identities' own FK on
	// servers(id)): the canonical id of this server's own current signing
	// key, shared by its private_keys row and its public_keys row alike.
	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(16) UNIQUE,
		name VARCHAR(255) PRIMARY KEY,
		self BOOLEAN NOT NULL DEFAULT FALSE,
		signing_key VARCHAR(255),
		identity_backup_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		base_url TEXT,
		connected BOOLEAN NOT NULL DEFAULT FALSE,
		fingerprint VARCHAR(255),
		revoked_at TIMESTAMP,
		revoked_by VARCHAR(255),
		revoked_reason TEXT,
		disconnect_requested_at TIMESTAMP,
		disconnect_requested_by VARCHAR(255),
		disconnect_reason TEXT
	);`

	// Normalized attestation rows. public_key_id is not FK'd to public_keys
	// (historical / rotated keys — a signature can reference a
	// since-superseded key id).
	createUserSignaturesTable := `
	CREATE TABLE IF NOT EXISTS user_signatures (
		id SERIAL PRIMARY KEY,
		public_key_id VARCHAR(255) NOT NULL,
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
	// always "{userID}@{serverID}".
	createIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS identities (
		id VARCHAR(255) PRIMARY KEY,
		server_id VARCHAR(16) REFERENCES servers(id) ON DELETE CASCADE,
		public_key_fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createIdentitiesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_identities_server_id
		ON identities(server_id);
	`

	// users is a satellite of identities — profile fields only. id IS
	// identities.id directly. user_fingerprint is the denormalized
	// active-key hint; the signing key lives on user_signatures.
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

	// Server-owned private keys. Unaffected by the public_keys unification
	// below — private key material never leaves this table, regardless of
	// whose key it is (a user's private key never touches the server at all).
	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		id VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		revoked_at TIMESTAMP,
		revoke_reason TEXT
	);`

	// Unified public key storage — every key this server holds the public
	// half of. owner FKs to identities for local users, NULL otherwise.
	// predecessor_id points at the key this one replaced (rotation only).
	createPublicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		id VARCHAR(255) PRIMARY KEY,
		owner VARCHAR(255) REFERENCES identities(id) ON DELETE CASCADE,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		server_signature_id INT NOT NULL UNIQUE REFERENCES server_signatures(id),
		predecessor_id VARCHAR(255) REFERENCES public_keys(id)
	);`

	createPublicKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_public_keys_owner
		ON public_keys(owner) WHERE owner IS NOT NULL;
	`

	// A row's existence means the key is revoked. successor and
	// successor_signature_id are written later, once a replacement key is
	// uploaded — the latter is the OLD key's signature over the new one.
	createPublicKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS public_key_revocations (
		revoked_id VARCHAR(255) PRIMARY KEY
			REFERENCES public_keys(id) ON DELETE CASCADE,
		reason TEXT,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		successor VARCHAR(255) REFERENCES public_keys(id),
		successor_signature_id INT REFERENCES user_signatures(id)
	);`

	// reed_identities is to reeds what identities is to users: a thin
	// pointer row other tables reference whether or not this server holds
	// the reed's actual content (local, or foreign learned via relay).
	createReedIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS reed_identities (
		id VARCHAR(255) PRIMARY KEY,
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createReedIdentitiesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_identities_server_id
		ON reed_identities(server_id);
	`

	// Tip reed metadata. Signature ids store the attestations so SignReed
	// retries can return the same countersignature (idempotent). reeds is
	// always the local-only satellite of reed_identities.
	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		private_key_id VARCHAR(255) NOT NULL REFERENCES private_keys(id),
		signed_at TIMESTAMP NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	`

	// echoing_* is the reed doing the echo; echoed_* is the reed it points at.
	// is_blank marks a bare re-share, captured once at insert since the server never stores content.
	// Author ids are stored directly since either side may be foreign to this server.
	createReedEchoesTable := `
	CREATE TABLE IF NOT EXISTS reed_echoes (
		echoing_reed_id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		echoed_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		echoing_author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		echoed_author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		is_blank BOOLEAN NOT NULL DEFAULT FALSE,
		signed_at TIMESTAMP NOT NULL
	);`

	createReedEchoesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_echoes_echoed_signed
		ON reed_echoes (echoed_reed_id, signed_at);
	`

	// id is the root reed ref; one row per thread, created on first reply.
	// reed_id FKs to reed_identities, not reeds directly, since a reply's
	// home server may relay just a reference rather than the content itself.
	createReedRepliesTable := `
	CREATE TABLE IF NOT EXISTS reed_replies (
		thread_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		parent_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		timestamp TIMESTAMP NOT NULL
	);`

	createReedRepliesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_timestamp
		ON reed_replies (parent_reed_id, timestamp);

	CREATE INDEX IF NOT EXISTS idx_reed_replies_thread
		ON reed_replies (thread_id, timestamp);
	`

	// One row per (reed, mentioned user). mentioning_reed_id FKs to
	// reed_identities, not reeds, since a foreign reed can mention a local
	// user. mentioned_user_id already carries the server in its canonical form, so no separate mentioned_server_id column is needed.
	createReedMentionsTable := `
	CREATE TABLE IF NOT EXISTS reed_mentions (
		mentioning_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		mentioned_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,

		PRIMARY KEY (mentioning_reed_id, mentioned_user_id)
	);`

	createReedMentionsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_mentions_mentioned
		ON reed_mentions (mentioned_user_id);

	CREATE INDEX IF NOT EXISTS idx_reed_mentions_reed
		ON reed_mentions (mentioning_reed_id);
	`

	// Signed reed-removal certificates. Source of truth for "gone"; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// PK is reed_id, which embeds the author — no separate user_id column.
	createReedRemovalsTable := `
	CREATE TABLE IF NOT EXISTS reed_removals (
		reed_id VARCHAR(255) PRIMARY KEY,
		public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

	// Signed account-removal certificates. One cert per user; public keys
	// remain. public_key_id binds the signing key (same class as reed
	// removals). note ≤140 enforced by CHECK + API.
	createAccountRemovalsTable := `
	CREATE TABLE IF NOT EXISTS account_removals (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		note VARCHAR(140) NOT NULL DEFAULT '',
		public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140)
	);`

	// Encrypted server->user mailbox messages. No plaintext columns — the
	// row's mere existence is the undelivered-message record; it is
	// deleted once the client ACKs receipt (see specs/notifications/03,04).
	createUserMailboxTable := `
	CREATE TABLE IF NOT EXISTS user_mailbox (
		id VARCHAR(255) PRIMARY KEY,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ciphertext TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT NOW()
	);`

	createUserMailboxIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_mailbox_user_created
		ON user_mailbox(user_id, created_at);
	`

	// Signed like certificates, one row per currently-liked (liker, reed)
	// pair; unliking hard-deletes the row. reed_id FKs to reed_identities,
	// not reeds, so a local user's like on a FOREIGN reed is represented here too.
	createReedsLikedTable := `
	CREATE TABLE IF NOT EXISTS reeds_liked (
		liker_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		liker_public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		PRIMARY KEY (liker_user_id, reed_id)
	);`

	createReedsLikedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_liked_liker_created
		ON reeds_liked (liker_user_id, user_signature_id DESC);
	CREATE INDEX IF NOT EXISTS idx_reeds_liked_reed
		ON reeds_liked (reed_id);
	`

	// //////////// //
	//   Ripples    //
	// //////////// //

	createRipplesTable := `
	CREATE TABLE IF NOT EXISTS ripples (
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reeds(id) ON DELETE CASCADE,
		expires_at TIMESTAMP NOT NULL
	);`

	createRipplesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripples_expires
		ON ripples (expires_at);
	`

	createRippleResponsesTable := `
	CREATE TABLE IF NOT EXISTS ripple_responses (
		id VARCHAR(64) PRIMARY KEY,
		reed_id VARCHAR(255) NOT NULL REFERENCES ripples(reed_id) ON DELETE CASCADE,
		thread_id UUID NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		content VARCHAR(140) NOT NULL,
		replying_to VARCHAR(64) REFERENCES ripple_responses(id) ON DELETE SET NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE,
		posted_at TIMESTAMP NOT NULL,

		user_fingerprint VARCHAR(255) NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

	createRippleResponsesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripple_responses_reed_thread_posted
		ON ripple_responses (reed_id, thread_id, posted_at);
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

	// holder_user_id is who holds the reed — always a genuine LOCAL user
	// (FKs to users, not identities). reed_id FKs to reed_identities since
	// a local user can be caching a FOREIGN reed's verified content.
	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		holder_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (holder_user_id, reed_id)
	);`

	// Lookups by reed use reed_id; holder is in the PK.
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_reed
		ON reed_allocations(reed_id);
	`

	// Records that a PEER SERVER (not a specific user) has told us one of
	// its own users holds a verified copy of reed_id — the fallback target
	// when no local holder is online for a reed we're home to.
	createReedServerAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_server_allocations (
		reed_id VARCHAR(255) NOT NULL
			REFERENCES reed_identities(id) ON DELETE CASCADE,
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (reed_id, server_id)
	);`

	createReedServerAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_server_allocations_reed
		ON reed_server_allocations(reed_id);
	`

	// tags are normalized hashtag names extracted at SignReed for pipe
	// fanout at PUBLISH_READY (pipes 01). Empty until claim deletes the row.
	createPendingFanoutTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_fanout (
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reeds(id) ON DELETE CASCADE,
		tags TEXT[] NOT NULL DEFAULT '{}'
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

	// One row per reed, kept in sync by triggers below instead of
	// recomputed live on every read. Rows are created lazily by the
	// trigger functions' upsert-increment, not seeded here.
	createReedStatsTable := `
	CREATE TABLE IF NOT EXISTS reed_stats (
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		reply_count INT NOT NULL DEFAULT 0,
		echo_count INT NOT NULL DEFAULT 0,
		like_count INT NOT NULL DEFAULT 0,
		holder_count INT NOT NULL DEFAULT 0
	);`

	// Coverage percent is a pure function of two already-cheap counters
	// (reed_stats.holder_count, network_stats.active_users) — a plain view,
	// not a trigger, since there's nothing expensive left to precompute.
	createReedCoverageView := `
	CREATE OR REPLACE VIEW reed_coverage AS
	SELECT
		rs.reed_id,
		rs.holder_count,
		LEAST(100, (100 * rs.holder_count) / GREATEST(1, ns.active_users)) AS coverage_percent
	FROM reed_stats rs
	CROSS JOIN network_stats ns;
	`

	// bump_reed_stat upserts reed_stats and adds delta to one column,
	// selected by col_name — shared by every simple (non-reply) counter
	// trigger below so the increment/decrement logic lives in one place.
	createBumpReedStatFunction := `
	CREATE OR REPLACE FUNCTION bump_reed_stat(p_reed_id VARCHAR(255), col_name TEXT, delta INT)
	RETURNS VOID AS $$
	BEGIN
		EXECUTE format(
			'INSERT INTO reed_stats (reed_id, %1$I) VALUES ($1, GREATEST(0, $2))
			 ON CONFLICT (reed_id) DO UPDATE SET %1$I = GREATEST(0, reed_stats.%1$I + $2)',
			col_name
		) USING p_reed_id, delta;
	END;
	$$ LANGUAGE plpgsql;
	`

	createEchoCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_echo_count_trigger() RETURNS TRIGGER AS $$
	BEGIN
		IF TG_OP = 'INSERT' AND NEW.echoing_reed_id != NEW.echoed_reed_id THEN
			PERFORM bump_reed_stat(NEW.echoed_reed_id, 'echo_count', 1);
		ELSIF TG_OP = 'DELETE' AND OLD.echoing_reed_id != OLD.echoed_reed_id THEN
			PERFORM bump_reed_stat(OLD.echoed_reed_id, 'echo_count', -1);
		END IF;
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createEchoCountTriggers := `
	DROP TRIGGER IF EXISTS reed_echoes_count_insert ON reed_echoes;
	CREATE TRIGGER reed_echoes_count_insert
		AFTER INSERT ON reed_echoes
		FOR EACH ROW EXECUTE FUNCTION reed_echo_count_trigger();

	DROP TRIGGER IF EXISTS reed_echoes_count_delete ON reed_echoes;
	CREATE TRIGGER reed_echoes_count_delete
		AFTER DELETE ON reed_echoes
		FOR EACH ROW EXECUTE FUNCTION reed_echo_count_trigger();
	`

	createLikeCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_like_count_trigger() RETURNS TRIGGER AS $$
	BEGIN
		IF TG_OP = 'INSERT' THEN
			PERFORM bump_reed_stat(NEW.reed_id, 'like_count', 1);
		ELSIF TG_OP = 'DELETE' THEN
			PERFORM bump_reed_stat(OLD.reed_id, 'like_count', -1);
		END IF;
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createLikeCountTriggers := `
	DROP TRIGGER IF EXISTS reeds_liked_count_insert ON reeds_liked;
	CREATE TRIGGER reeds_liked_count_insert
		AFTER INSERT ON reeds_liked
		FOR EACH ROW EXECUTE FUNCTION reed_like_count_trigger();

	DROP TRIGGER IF EXISTS reeds_liked_count_delete ON reeds_liked;
	CREATE TRIGGER reeds_liked_count_delete
		AFTER DELETE ON reeds_liked
		FOR EACH ROW EXECUTE FUNCTION reed_like_count_trigger();
	`

	createHolderCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_holder_count_trigger() RETURNS TRIGGER AS $$
	BEGIN
		IF TG_OP = 'INSERT' THEN
			PERFORM bump_reed_stat(NEW.reed_id, 'holder_count', 1);
		ELSIF TG_OP = 'DELETE' THEN
			PERFORM bump_reed_stat(OLD.reed_id, 'holder_count', -1);
		END IF;
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createHolderCountTriggers := `
	DROP TRIGGER IF EXISTS reed_allocations_count_insert ON reed_allocations;
	CREATE TRIGGER reed_allocations_count_insert
		AFTER INSERT ON reed_allocations
		FOR EACH ROW EXECUTE FUNCTION reed_holder_count_trigger();

	DROP TRIGGER IF EXISTS reed_allocations_count_delete ON reed_allocations;
	CREATE TRIGGER reed_allocations_count_delete
		AFTER DELETE ON reed_allocations
		FOR EACH ROW EXECUTE FUNCTION reed_holder_count_trigger();
	`

	// Walks parent_reed_id from start_reed_id up to the root, applying
	// delta to reply_count at every level.
	createBumpReplyAncestorsFunction := `
	CREATE OR REPLACE FUNCTION bump_reply_ancestors(start_reed_id VARCHAR(255), delta INT)
	RETURNS VOID AS $$
	DECLARE
		current_id VARCHAR(255);
		next_id VARCHAR(255);
	BEGIN
		current_id := start_reed_id;
		LOOP
			SELECT parent_reed_id INTO next_id FROM reed_replies WHERE reed_id = current_id;
			EXIT WHEN next_id IS NULL;
			PERFORM bump_reed_stat(next_id, 'reply_count', delta);
			current_id := next_id;
		END LOOP;
	END;
	$$ LANGUAGE plpgsql;
	`

	// A new reply increments every ancestor's reply_count, unless the
	// replying author's account is already removed — mirrors
	// GetSubtreeReplyCount's live NOT EXISTS account_removals filter.
	createReplyCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_reply_count_trigger() RETURNS TRIGGER AS $$
	DECLARE
		author_removed BOOLEAN;
	BEGIN
		SELECT EXISTS(
			SELECT 1 FROM account_removals ar
			JOIN reeds r ON r.id = NEW.reed_id
			WHERE ar.user_id = r.user_id
		) INTO author_removed;
		IF NOT author_removed THEN
			PERFORM bump_reply_ancestors(NEW.reed_id, 1);
		END IF;
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createReplyCountTrigger := `
	DROP TRIGGER IF EXISTS reed_replies_count_insert ON reed_replies;
	CREATE TRIGGER reed_replies_count_insert
		AFTER INSERT ON reed_replies
		FOR EACH ROW EXECUTE FUNCTION reed_reply_count_trigger();
	`

	// Starts from OLD.parent_reed_id, not OLD.reed_id — by AFTER DELETE
	// time OLD's own row is gone, so bump_reply_ancestors(OLD.reed_id, ...)
	// would find nothing and silently do nothing.
	createReedReplyDeleteTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_reply_delete_count_trigger() RETURNS TRIGGER AS $$
	BEGIN
		PERFORM bump_reed_stat(OLD.parent_reed_id, 'reply_count', -1);
		PERFORM bump_reply_ancestors(OLD.parent_reed_id, -1);
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createReedReplyDeleteTrigger := `
	DROP TRIGGER IF EXISTS reed_replies_count_delete ON reed_replies;
	CREATE TRIGGER reed_replies_count_delete
		AFTER DELETE ON reed_replies
		FOR EACH ROW EXECUTE FUNCTION reed_reply_delete_count_trigger();
	`

	// A reed removal decrements every ancestor's reply_count, undoing what
	// the insert trigger added — a no-op walk for a removed root reed
	// (reed_replies has no row for it, so the loop exits immediately).
	createReedRemovalReplyCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION reed_removal_reply_count_trigger() RETURNS TRIGGER AS $$
	BEGIN
		PERFORM bump_reply_ancestors(NEW.reed_id, -1);
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createReedRemovalReplyCountTrigger := `
	DROP TRIGGER IF EXISTS reed_removals_count_insert ON reed_removals;
	CREATE TRIGGER reed_removals_count_insert
		AFTER INSERT ON reed_removals
		FOR EACH ROW EXECUTE FUNCTION reed_removal_reply_count_trigger();
	`

	// Decrements every reply by this author not already covered by its
	// own reed_removals row — O(replies × depth), the one bulk trigger.
	createAccountRemovalReplyCountTriggerFunction := `
	CREATE OR REPLACE FUNCTION account_removal_reply_count_trigger() RETURNS TRIGGER AS $$
	DECLARE
		reply_row RECORD;
	BEGIN
		FOR reply_row IN
			SELECT rr.reed_id
			FROM reed_replies rr
			JOIN reeds r ON r.id = rr.reed_id
			WHERE r.user_id = NEW.user_id
			AND NOT EXISTS (
				SELECT 1 FROM reed_removals rm WHERE rm.reed_id = rr.reed_id
			)
		LOOP
			PERFORM bump_reply_ancestors(reply_row.reed_id, -1);
		END LOOP;
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql;
	`

	createAccountRemovalReplyCountTrigger := `
	DROP TRIGGER IF EXISTS account_removals_count_insert ON account_removals;
	CREATE TRIGGER account_removals_count_insert
		AFTER INSERT ON account_removals
		FOR EACH ROW EXECUTE FUNCTION account_removal_reply_count_trigger();
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

	// reed_id FKs to reed_identities, not reeds directly — a pending event
	// can be about a FOREIGN reed, so this table uniformly represents both
	// local and foreign subjects the same way.
	createPendingReedEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_reed_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE
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

	// Originating-server bookkeeping: maps a local pending event to the
	// outstanding peer registration on the reed's home server (which peer
	// to call back, and what id THEY know this event by).
	createForeignPendingEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS foreign_pending_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		home_server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		peer_event_id VARCHAR(255) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createForeignPendingEventsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_foreign_pending_events_home_server
		ON foreign_pending_events(home_server_id);
	`

	// Home-server bookkeeping: records which peer+user a sentinel-attributed
	// pending_events row was actually registered on behalf of.
	createForeignRelayRequestsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS foreign_relay_requests (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		requesting_server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		requesting_user_id VARCHAR(255) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createForeignRelayRequestsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_foreign_relay_requests_server
		ON foreign_relay_requests(requesting_server_id);
	`

	createProfileSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS profile_subscriptions (
		subscription_id VARCHAR(255) PRIMARY KEY,
		viewer_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		author_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (viewer_user_id, author_user_id)
	);`

	createProfileSubscriptionsIndex := `
	CREATE INDEX IF NOT EXISTS idx_profile_subscriptions_viewer
		ON profile_subscriptions(viewer_user_id);
	CREATE INDEX IF NOT EXISTS idx_profile_subscriptions_author
		ON profile_subscriptions(author_user_id);
	`

	// reed_id FKs to reed_identities (not reeds directly) so a viewer can
	// durably subscribe to a foreign reed's stats too — mirrors
	// profile_subscriptions' own local-vs-foreign-agnostic shape.
	createReedSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS reed_subscriptions (
		subscription_id VARCHAR(255) PRIMARY KEY,
		viewer_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createReedSubscriptionsIndex := `
	CREATE INDEX IF NOT EXISTS idx_reed_subscriptions_viewer
		ON reed_subscriptions(viewer_user_id);
	CREATE INDEX IF NOT EXISTS idx_reed_subscriptions_reed
		ON reed_subscriptions(reed_id);
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

	// Invites — operational redeem state. id is canonical
	// (creatorID@serverID/uuid), self-describing and globally unique, so
	// it alone is PK; created_by stays a real column for CountByCreator
	// and cascade-on-account-removal.
	createInvitesTable := `
	CREATE TABLE IF NOT EXISTS invites (
		id VARCHAR(255) PRIMARY KEY,
		created_by VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		token_hash BYTEA NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		claimed_at TIMESTAMPTZ,
		claimed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		revoked_at TIMESTAMPTZ,
		granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (granted_role IN ('admin', 'user'))
	);`

	createInvitesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_invites_created_by
		ON invites(created_by);
	`

	// federation_invitation covers the whole handshake lifecycle: new ->
	// accepted/canceled, accepted -> approved/rejected, approved -> revoked.
	// fingerprint/public_key_armor are the peer's unverified key, promoted into public_keys/servers only once approved.
	createFederationInvitationTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation (
		id VARCHAR(255) PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		secret_hash BYTEA NOT NULL,
		fingerprint VARCHAR(255) NOT NULL,
		public_key_armor TEXT NOT NULL,
		created_by VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		accepted_at TIMESTAMPTZ,
		server_id VARCHAR(16) REFERENCES servers(id) ON DELETE SET NULL,
		status VARCHAR(16) NOT NULL DEFAULT 'new'
			CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
		reviewed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		reviewed_at TIMESTAMPTZ,
		connection_ciphertext TEXT
	);`

	// One row per handshake attempt against a peer, permanent (never
	// deleted on approve/reject — it's the audit trail). server_id is set
	// only once APPROVED; invitation_id is set on the INITIATOR side only.
	createFederationAttemptTable := `
	CREATE TABLE IF NOT EXISTS federation_attempt (
		id VARCHAR(255) PRIMARY KEY,
		remote_server_id VARCHAR(16) NOT NULL,
		remote_server_name VARCHAR(255) NOT NULL,
		base_url TEXT NOT NULL,
		fingerprint VARCHAR(255) NOT NULL,
		invitation_id VARCHAR(255) REFERENCES federation_invitation(id),
		server_id VARCHAR(16) REFERENCES servers(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		status VARCHAR(16) NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'approved', 'rejected')),
		approved_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		approved_at TIMESTAMPTZ,
		rejected_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		rejected_at TIMESTAMPTZ,
		rejected_reason TEXT
	);`

	// Generic append-only log line, not itself tied to an invitation,
	// attempt, or server — three junction tables link a line to whichever
	// it's about, so an admin can see what actually happened.
	createFederationLogTable := `
	CREATE TABLE IF NOT EXISTS federation_log (
		id VARCHAR(255) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		level VARCHAR(16) NOT NULL
			CHECK (level IN ('info', 'error')),
		message TEXT NOT NULL
	);`

	// The INITIATOR logs against its invitation from the moment a connect
	// callback arrives — invitation's server_id isn't set until approval,
	// so pre-acceptance rejections have nothing else to log against yet.
	createFederationInvitationLogTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation_log (
		invitation_id VARCHAR(255) NOT NULL REFERENCES federation_invitation(id) ON DELETE CASCADE,
		log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,

		PRIMARY KEY (invitation_id, log_id)
	);`

	// Attempt-scoped log lines — handshake verification through the
	// approve/reject decision. Unlike federation_server_log, this survives
	// rejection, so a rejection reason has somewhere permanent to live.
	createFederationAttemptLogTable := `
	CREATE TABLE IF NOT EXISTS federation_attempt_log (
		attempt_id VARCHAR(255) NOT NULL REFERENCES federation_attempt(id) ON DELETE CASCADE,
		log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,

		PRIMARY KEY (attempt_id, log_id)
	);`

	// federation_server_log: server-scoped log lines, for activity AFTER a
	// servers row exists (i.e. after a federation_attempt was approved) —
	// pre-approval activity belongs in federation_attempt_log instead.
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

		createPrivateKeysTable,

		createPublicKeysTable,
		createPublicKeyIndexes,

		createPublicKeyRevocationsTable,

		createReedIdentitiesTable,
		createReedIdentitiesIndexes,

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

		createUserMailboxTable,
		createUserMailboxIndexes,

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
		createInvitesIndexes,

		// Realtime
		createOnlineUsersTable,

		createBroadcastSubscriptionsTable,
		createBroadcastSubscriptionIndexes,

		createReedAllocationsTable,
		createReedAllocationIndexes,

		createReedServerAllocationsTable,
		createReedServerAllocationIndexes,

		createPendingFanoutTable,

		createNetworkStatsTable,
		seedNetworkStats,

		createReedStatsTable,
		createReedCoverageView,

		createBumpReedStatFunction,

		createEchoCountTriggerFunction,
		createEchoCountTriggers,

		createLikeCountTriggerFunction,
		createLikeCountTriggers,

		createHolderCountTriggerFunction,
		createHolderCountTriggers,

		createBumpReplyAncestorsFunction,

		createReplyCountTriggerFunction,
		createReplyCountTrigger,

		createReedReplyDeleteTriggerFunction,
		createReedReplyDeleteTrigger,

		createReedRemovalReplyCountTriggerFunction,
		createReedRemovalReplyCountTrigger,

		createAccountRemovalReplyCountTriggerFunction,
		createAccountRemovalReplyCountTrigger,

		createProfileSubscriptionsTable,
		createProfileSubscriptionsIndex,

		createReedSubscriptionsTable,
		createReedSubscriptionsIndex,

		createPendingEventsTable,
		createPendingEventsIndexes,

		createPendingReedEventsTable,
		createPendingReedEventsIndexes,

		createPendingAccountEventsTable,

		createForeignPendingEventsTable,
		createForeignPendingEventsIndexes,

		createForeignRelayRequestsTable,
		createForeignRelayRequestsIndexes,

		// Recovery
		createUnclaimedAccountsTable,

		createOngoingRecoveriesTable,

		createPendingFollowsTable,
		createPendingFollowsIndexes,

		// Federation
		createFederationInvitationTable,
		createFederationAttemptTable,
		createFederationLogTable,
		createFederationInvitationLogTable,
		createFederationAttemptLogTable,
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
