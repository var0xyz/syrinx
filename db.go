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

// KeyPredecessor identifies the key this one replaced (rotation only).
// The predecessor's detached signature over this key's armor — proof the
// old key approved the handoff — is NOT here: that's revocation-certificate
// data, it lives on the predecessor's own KeyRevocation.SuccessorSignature.
type KeyPredecessor struct {
	ID string `json:"id"`
}

// Key is the wire shape of a public key — a local user's or this server's
// own (peer servers' keys are never stored; every foreign id proxies live
// to that peer instead, see main.go's GET /keys/{id}).
// `ServerSignature` is required: the countersignature over
// (userID, id, armor). `Revoked` is computed on read from
// public_key_revocations — never stored on public_keys.
//
// `Predecessor` is set for rotation keys only; signup/bootstrap keys
// return null.
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
// for use by countersign operations (h.signingKey) — never serialized as
// wire JSON itself, so it's a dedicated internal type rather than reusing
// Key: Fingerprint here is deliberately the bare fingerprint (what every
// ServerSignature.Fingerprint / X-Syrinx-* header wants), not a Key.ID
// canonical public_keys row id. Armor is the DECRYPTED PRIVATE key armor
// (used to produce signatures), never exposed over the wire.
type ServerSigningKey struct {
	Fingerprint string
	Armor       string
	CreatedAt   time.Time
}

// KeyRevocation is the wire shape of a signed revocation attestation.
// ID is the revoked key's own id (public_key_revocations.revoked_id is
// both this cert's primary key and its FK to public_keys.id — a key can
// only be revoked once, so the two roles collapse onto one column/field).
// The user signature covers (userID, id, reason); the server
// countersignature binds that user attestation and supplies the
// authoritative revoke time as serverSignature.timestamp.
//
// Successor and SuccessorSignature are bookkeeping written later by
// AddPublicKey when the replacement key is uploaded — both are unknown at
// revoke time, so both are nil until then. SuccessorSignature is the
// revoked key's own detached signature over the successor's armor (the
// rotation handoff proof); it and Successor are covered by neither the
// user nor the server signature on this cert.
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
	// base_url/connected/fingerprint/revoked are federation fields: unset/
	// false on the self row. A federated peer row is written ONLY by
	// ApproveFederationAttempt (see services.go) — the handshake itself
	// lives entirely on federation_attempt until a second admin approves;
	// see specs/federation/03. fingerprint is the peer's pinned server
	// signing key fingerprint from that approved attempt — the trust root
	// for verifying peer-authenticated runtime requests (specs/federation/04).
	// Its armor is NEVER stored locally: every fetch of a peer's key (or
	// any of that peer's users' keys, or profile) proxies live to base_url
	// and relays the response — see main.go's GET /keys/{id} route and
	// handlers.go's proxyToPeer. public_keys only ever holds LOCAL keys
	// (this server's own + its local users').
	// revoked is set by a future de-establish step (specs/federation/05);
	// a revoked peer's incoming requests must be rejected even though the
	// row (and its audit trail) stays.
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
		revoked BOOLEAN NOT NULL DEFAULT FALSE
	);`

	// Normalized attestation rows (signatures proposal 01). Entities will
	// FK here in later migrate steps; public_key_id is not FK'd to
	// public_keys (historical / rotated keys — a signature can reference a
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

	// Server-owned private keys. Unaffected by the public_keys unification
	// below — private key material never leaves this table, regardless of
	// whose key it is (a user's private key never touches the server at all).
	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		revoked_at TIMESTAMP,
		revoke_reason TEXT
	);`

	// Unified public key storage — every key this server holds the public
	// half of, whether a user's or this server's own. id is canonical:
	// "{userID}@{serverID}/{fingerprint}" for a user key,
	// "{fingerprint}@{serverID}" for this server's own signing key.
	// Peer (non-self) servers' keys are NEVER stored here — every request
	// for a foreign id is proxied live to that peer (see main.go's /keys
	// route and handlers.go's proxyToPeer), so this table only ever holds
	// local users' keys plus this server's own.
	//
	// owner is a real FK to identities for local users; NULL for this
	// server's own key (no identities row) and for remote/federated users'
	// keys (no local identities row to reference) — the canonical id itself
	// remains the source of truth for ownership either way (recover it via
	// identity.ParseKeyFingerprint).
	//
	// server_signature_id is NOT NULL and UNIQUE: every key carries exactly
	// one countersignature, including this server's own key, which is
	// countersigned by itself at boot (see InitServerKey) — redundant for
	// that one row, but keeps every row in this table uniformly verifiable
	// the same way, no special-casing in read paths.
	//
	// predecessor_id points at the key this one replaced (rotation only,
	// NULL for a signup/bootstrap key). The predecessor's SIGNATURE over
	// this row's armor — proof the old key approved the handoff — lives on
	// the predecessor's OWN revocation row (successor_signature_id in
	// public_key_revocations below), not here: that's revocation-certificate
	// data, not a property of the new key itself.
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

	// Revocation attestation for a public key. A row's existence means the
	// key is revoked. revoked_id identifies which key (PK + FK to
	// public_keys, canonical + self-scoping). user_signature_id signs the
	// certificate itself (revoked_id + reason); server_signature_id
	// countersigns it; revoke time is server_signatures.signed_at.
	//
	// successor and successor_signature_id are both written later, when the
	// replacement key is uploaded via AddPublicKey (the client revokes
	// first and adds the new key second, so at RevokeKey time neither is
	// known yet): successor is the plain forward pointer to the new key;
	// successor_signature_id is this (the OLD, revoked) key's detached
	// signature over the NEW key's armor — the rotation handoff proof a
	// verifier walks back through when following a predecessor_id chain.
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
	// pointer row (canonical id + home server) that other tables can
	// reference regardless of whether this server has ever signed the
	// reed's actual content. A row exists here for every LOCAL reed (see
	// createReedsTable's FK below) and for every FOREIGN reed this server
	// has learned about via the cross-server relay bridge (REQUEST_REED/
	// SUBSCRIBE_PROFILE — see realtime/service.go). Whether a given id is
	// local is answered by checking for a matching reeds row (same
	// pattern ReedExists already uses), not a separate column here —
	// reeds.id and reed_identities.id are the same value for local
	// content, so a redundant link column could only ever disagree with
	// the join, never add information.
	createReedIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS reed_identities (
		id VARCHAR(255) PRIMARY KEY,
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createReedIdentitiesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_identities_server_id
		ON reed_identities(server_id);
	`

	// Tip reed metadata. private_key_fingerprint is the server key used
	// for the countersignature. user_signature_id / server_signature_id
	// store the attestations so SignReed retries can return the same
	// countersignature (idempotent). id FKs to reed_identities the same
	// way users.id FKs to identities — reeds is always the local-only,
	// heavyweight satellite; reed_identities is the reference layer other
	// tables should point at when the reed itself might be foreign.
	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		allocation_count INT NOT NULL DEFAULT 0,
		like_count INT NOT NULL DEFAULT 0
	);`

	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
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
		echoing_reed_id VARCHAR(255) NOT NULL,
		echoed_reed_id VARCHAR(255) NOT NULL,
		is_blank BOOLEAN NOT NULL DEFAULT FALSE,
		signed_at TIMESTAMP NOT NULL,

		PRIMARY KEY (echoing_reed_id),
		FOREIGN KEY (echoing_reed_id) REFERENCES reeds(id),
		FOREIGN KEY (echoed_reed_id) REFERENCES reed_identities(id)
	);`

	createReedEchoesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_echoes_echoed_signed
		ON reed_echoes (echoed_reed_id, signed_at);
	`

	// id is the root reed ref (user@server/reed); one row per thread (created on first reply).
	createReedRepliesTable := `
	CREATE TABLE IF NOT EXISTS reed_replies (
		thread_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) NOT NULL,
		parent_reed_id VARCHAR(255) NOT NULL,
		timestamp TIMESTAMP NOT NULL,

		PRIMARY KEY (reed_id),
		FOREIGN KEY (reed_id) REFERENCES reeds(id),
		FOREIGN KEY (parent_reed_id) REFERENCES reed_identities(id)
	);`

	createReedRepliesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_timestamp
		ON reed_replies (parent_reed_id, timestamp);

	CREATE INDEX IF NOT EXISTS idx_reed_replies_thread
		ON reed_replies (thread_id, timestamp);
	`

	// One row per (reed, mentioned user). mentioning_reed_id = reed that
	// contains the @ — FKs to reed_identities, not reeds directly, since a
	// FOREIGN reed (authored on a peer) can mention a local user; this
	// server needs a row to represent that once cross-server mention
	// notification exists (mentioning_reed_id may not be one of this
	// server's own reeds). mentioned_user_id FKs to identities(id), which
	// can hold a provisional remote identity, and already carries the
	// mentioned server in its own canonical userID@serverID form — no
	// separate mentioned_server_id column needed, it was always
	// redundant with that.
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

	// Signed reed-removal certificates. Source of truth for “gone”; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// PK is reed_id, which embeds the author — no separate user_id column.
	// public_key_id binds the signing key (canonical, self-scoping —
	// single-column FK to public_keys); signatures via FKs.
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

	// Signed like certificates, one row per currently-liked (liker, reed)
	// pair; unliking hard-deletes the row. liker_public_key_id binds the
	// signing key (same class as reed removals). reed_id FKs to
	// reed_identities, not reeds, so a local user's like on a FOREIGN
	// reed has a row to represent it here too (the home server verifies
	// and countersigns the like; this server mirrors it locally once
	// that's confirmed).
	createReedsLikedTable := `
	CREATE TABLE IF NOT EXISTS reeds_liked (
		liker_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL,
		liker_public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		PRIMARY KEY (liker_user_id, reed_id),
		FOREIGN KEY (reed_id) REFERENCES reed_identities(id)
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
		reed_id VARCHAR(255) PRIMARY KEY,
		expires_at TIMESTAMP NOT NULL,

		FOREIGN KEY (reed_id) REFERENCES reeds(id)
			ON DELETE CASCADE
	);`

	createRipplesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripples_expires
		ON ripples (expires_at);
	`

	createRippleResponsesTable := `
	CREATE TABLE IF NOT EXISTS ripple_responses (
		id VARCHAR(64) PRIMARY KEY,
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

		FOREIGN KEY (reed_id) REFERENCES ripples(reed_id)
			ON DELETE CASCADE
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

	// holder_user_id is who holds the reed; reed_id FKs to reed_identities
	// (not reeds directly) since a holder can be caching a FOREIGN reed's
	// verified content — this is the original motivating case for
	// reed_identities existing at all.
	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL,
		holder_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (holder_user_id, reed_id),
		FOREIGN KEY (reed_id)
			REFERENCES reed_identities(id) ON DELETE CASCADE
	);`

	// Lookups by reed use reed_id; holder is in the PK.
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_reed
		ON reed_allocations(reed_id);
	`

	// tags are normalized hashtag names extracted at SignReed for pipe
	// fanout at PUBLISH_READY (pipes 01). Empty until claim deletes the row.
	createPendingFanoutTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_fanout (
		reed_id VARCHAR(255) NOT NULL,
		tags    TEXT[] NOT NULL DEFAULT '{}',

		PRIMARY KEY (reed_id),
		FOREIGN KEY (reed_id)
			REFERENCES reeds(id) ON DELETE CASCADE
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

	// reed_id FKs to reed_identities, not reeds directly — a pending event
	// can be about a FOREIGN reed (a viewer on this server relaying
	// content from a peer's author), so this table now uniformly
	// represents both local and foreign subjects the same way. This is
	// what let foreign_pending_events (below) drop its own reed_id column.
	createPendingReedEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_reed_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL,

		FOREIGN KEY (reed_id)
			REFERENCES reed_identities(id) ON DELETE CASCADE
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

	// Originating-server bookkeeping: maps a local REQUEST_REED/profile
	// subscription's pending event to the outstanding peer registration on
	// the reed's home server (which peer to call back, and what id THEY
	// know this event by). No reed_id column here anymore — the subject
	// is a normal pending_reed_events row now that reed_id can name a
	// foreign reed directly; this table only carries what
	// pending_reed_events structurally can't (the peer-relay mapping).
	createForeignPendingEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS foreign_pending_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		home_server_id VARCHAR(16) NOT NULL REFERENCES servers(id),
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
		requesting_server_id VARCHAR(16) NOT NULL REFERENCES servers(id),
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
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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
	// server_id is nullable and only set once a federation_attempt against
	// this invitation is APPROVED (a servers row only exists past that
	// point — see federation_attempt below); it is not set at handshake
	// acceptance time.
	// fingerprint/public_key_armor are the PEER's claimed key, supplied
	// out-of-band by the admin creating the invitation — unverified,
	// pre-trust bootstrap material. It stays here, NOT in public_keys
	// (which only ever holds verified/approved local or promoted keys),
	// until the handshake is accepted and a second admin approves the
	// resulting federation_attempt — only then does anything get promoted
	// into public_keys/servers.
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
		server_id VARCHAR(16) REFERENCES servers(id),
		status VARCHAR(16) NOT NULL DEFAULT 'new'
			CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
		reviewed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		reviewed_at TIMESTAMPTZ,
		connection_ciphertext TEXT
	);`

	// federation_attempt: one row per handshake attempt against a peer,
	// permanent (never deleted on approve/reject — it's the audit trail:
	// who created it, who approved/rejected it, and why). Created the
	// moment a handshake verifies — on the RESPONDER when the connection
	// string is redeemed, on the INITIATOR when the connect callback
	// verifies — well before any approval. remote_server_id/
	// remote_server_name/base_url/fingerprint are the peer's own claims
	// from the handshake payload, captured here rather than on servers,
	// because a servers row doesn't exist yet and may never (rejected) or
	// may differ across multiple attempts from the same peer over time.
	// server_id is nullable, set only once APPROVED, when the real servers
	// row is created (see ApproveFederationAttempt) — same nullable-until-
	// approved pattern as federation_invitation.server_id above.
	// invitation_id is set on the INITIATOR side (it has a local invitation
	// row); NULL on the RESPONDER (see OutgoingFederationAttempt — it never
	// has one).
	createFederationAttemptTable := `
	CREATE TABLE IF NOT EXISTS federation_attempt (
		id VARCHAR(255) PRIMARY KEY,
		remote_server_id VARCHAR(16) NOT NULL,
		remote_server_name VARCHAR(255) NOT NULL,
		base_url TEXT NOT NULL,
		fingerprint VARCHAR(255) NOT NULL,
		invitation_id VARCHAR(255) REFERENCES federation_invitation(id),
		server_id VARCHAR(16) REFERENCES servers(id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		status VARCHAR(16) NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'approved', 'rejected')),
		approved_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		approved_at TIMESTAMPTZ,
		rejected_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		rejected_at TIMESTAMPTZ,
		rejected_reason TEXT
	);`

	// federation_log: generic append-only log line, not itself tied to an
	// invitation, attempt, or server — three junction tables link a line
	// to whichever it's about. Handshake steps happen asynchronously
	// across two servers (connect callbacks, outbound POSTs that may fail
	// or time out), so this is how an admin sees what actually happened
	// instead of a link silently never progressing.
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
	// invitation's server_id isn't set until approval, so pre-acceptance
	// rejections (bad secret, wrong status, bad signature) have nothing
	// else to log against yet.
	createFederationInvitationLogTable := `
	CREATE TABLE IF NOT EXISTS federation_invitation_log (
		invitation_id VARCHAR(255) NOT NULL REFERENCES federation_invitation(id) ON DELETE CASCADE,
		log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,

		PRIMARY KEY (invitation_id, log_id)
	);`

	// federation_attempt_log: attempt-scoped log lines — handshake
	// verification, approve/reject, everything before (and including) the
	// approve/reject decision itself. Both initiator and responder log
	// here once their federation_attempt row exists. Unlike
	// federation_server_log, this survives rejection (federation_attempt
	// is never deleted), so a rejection reason has somewhere permanent to
	// live.
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

		createVerifiedIdentitiesView,

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
