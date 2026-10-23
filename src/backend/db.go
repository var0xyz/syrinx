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
	Invite          *Invite         `json:"invite"`
	Permissions     []string        `json:"permissions"`
}

// UserInfo is the unsigned, frequently changing view of a user
// (GET /users/{id}/info). ProfileTimestamp matches the user's current
// profile serverSignature.timestamp so clients can invalidate a cached profile.
type UserInfo struct {
	ID string `json:"id"`
	// Oldest surviving reed by this user, or nil when they have none.
	// Clients use it both as the "no reeds" signal and to detect the end
	// of the feed without waiting on a page ack.
	FirstReedID      *string   `json:"firstReedId"`
	FollowersCount   int       `json:"followersCount"`
	FollowingCount   int       `json:"followingCount"`
	ActiveKeyID      string    `json:"activeKeyID"`
	ProfileTimestamp time.Time `json:"profileTimestamp"`

	// TODO: In the future we need to make pinned reeds part of the user profile,
	// so that in case of recovery the pinned reeds are not lost. Not critical
	// for now though.
	PinnedReedIDs []string `json:"pinnedReedIDs,omitempty"`

	// Ids of the live vouches naming this user. Ids only: the client
	// fetches and verifies each cert before any mark is drawn from it.
	VouchIDs []string `json:"vouchIDs,omitempty"`
}

// Invite is the durable invite binding nested on User wire when set. ID is
// the invite's id (the value the profile signature covers); UserID/Username
// identify the inviter for display.
type Invite struct {
	ID       string `json:"id"`
	UserID   string `json:"userID"`
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

// adminRequest is a request as stored. Targets are every signed target,
// sorted; Dropped are those whose accounts were removed while pending.
type adminRequest struct {
	ID                 string
	Action             string
	RequesterID        string
	Reason             string
	Targets            []string
	Dropped            []string
	Body               string
	CreatedAt          time.Time
	RequesterSignature UserSignature
	// RequesterServerSignature is the server's, from when it was opened.
	RequesterServerSignature ServerSignature
	State                    string
	Decision                 *adminDecision
	ServerSignature          *ServerSignature
}

// adminDecision is the signed approval, rejection or withdrawal that
// closed a request.
type adminDecision struct {
	Decision  string
	DeciderID string
	Signature UserSignature
}

// Key is the wire shape of a public key — a local user's or this server's
// own (peer servers' keys are never stored; foreign ids proxy live instead).
// Revoked is computed on read; Predecessor is the replaced key's id (rotation only).
type Key struct {
	ID              string          `json:"id"`
	UserID          string          `json:"userID"`
	Armor           string          `json:"armor"`
	CreatedAt       time.Time       `json:"createdAt"`
	Revoked         bool            `json:"revoked"`
	Predecessor     *string         `json:"predecessor"`
	ServerSignature ServerSignature `json:"serverSignature"`
	// Set on revoked server keys only; the signed record is the key chain.
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	Compromised bool       `json:"compromised,omitempty"`
	// Restricted is a key an admin rebind bound: it may only rotate away.
	Restricted bool `json:"-"`
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
	// Admin is set on a lost-device rebind, signed in place of the key.
	Admin *adminRequest `json:"-"`
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
	Admin           *adminRequest   `json:"-"`
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

// BlockCert is the stored and wire shape of a block: the blocking user's
// signature plus their home server's countersignature.
type BlockCert struct {
	Type            string          `json:"type"`
	UserID          string          `json:"userID"`
	BlockedUserID   string          `json:"blockedUserID"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// VouchCert is the stored and wire shape of a signed vouch: one user
// attesting they compared fingerprints and the subject holds that key.
// The server serves the attestation and nothing else: whether it still
// counts depends on key state the client already resolves and verifies.
type VouchCert struct {
	ID              string          `json:"id"`
	VoucherUserID   string          `json:"voucherUserID"`
	VoucherKeyID    string          `json:"voucherKeyID"`
	SubjectUserID   string          `json:"subjectUserID"`
	SubjectKeyID    string          `json:"subjectKeyID"`
	Note            string          `json:"note"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
	// Present only on a retracted vouch; its presence is the withdrawal.
	// The time comes from the countersignature inside.
	Withdrawal *VouchWithdrawal `json:"withdrawal,omitempty"`
}

// VouchWithdrawal is a retraction: the voucher's signature plus the
// server's countersignature. Both are required — one the voucher never
// signed would be the server nullifying a vouch it dislikes.
type VouchWithdrawal struct {
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
	// What a server is, ours (self) and every peer's. key_id is a soft
	// reference to public_keys(id), not FK'd: servers is created before
	// public_keys to satisfy identities' own FK on servers(id), a real
	// three-way cycle. On our row it is our current signing key, shared by
	// its private_keys and public_keys rows; on a peer's, the key pinned at
	// approval. A peering's state lives in server_mesh.
	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(16) PRIMARY KEY,
		name VARCHAR(255) NOT NULL UNIQUE,
		self BOOLEAN NOT NULL DEFAULT FALSE,
		key_id VARCHAR(255),
		base_url TEXT,
		-- Where the server's users open links: ALLOWED_ORIGIN on our own row,
		-- as the peer reports it on a peer's.
		frontend_url TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// One row per peer, from our approval on. A purge removes the peer's
	// users and reeds but keeps this row and its revocation (purged_at).
	createServerMeshTable := `
	CREATE TABLE IF NOT EXISTS server_mesh (
		server_id VARCHAR(16) PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
		connected BOOLEAN NOT NULL DEFAULT TRUE,
		-- Who invited it and when, when we were the side that sent the invitation.
		invited_by VARCHAR(255),
		invited_at TIMESTAMP,
		approved_by VARCHAR(255),
		approved_at TIMESTAMP,
		-- Set once the peer is seen to have approved us too.
		peer_approved_at TIMESTAMP,
		-- The key of ours this peer last accepted, so a rotation is announced
		-- to each peer once.
		peer_key_ack VARCHAR(255),
		revoked_at TIMESTAMP,
		revoked_by VARCHAR(255),
		revoked_reason TEXT,
		disconnect_requested_at TIMESTAMP,
		disconnect_requested_by VARCHAR(255),
		disconnect_reason TEXT,
		-- Set by the peer's shutdown notice, cleared by its boot notice.
		down_at TIMESTAMP,
		purged_at TIMESTAMP
	);`

	createServersIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_servers_self
		ON servers (self) WHERE self;
	`

	// What applies only to this server. first_boot_at is when this database
	// was initialized: a first boot, or an identity import into an empty
	// database. It never changes.
	createServerSelfTable := `
	CREATE TABLE IF NOT EXISTS server_self (
		server_id VARCHAR(16) NOT NULL UNIQUE REFERENCES servers(id),
		-- The last ops export-identity, which carries every signing key.
		identity_backup_at TIMESTAMP,
		first_boot_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`

	// Normalized attestation rows. public_key_id/private_key_id are not
	// FK'd to public_keys/private_keys (historical/rotated/peer keys — a
	// signature can reference a key id that's since superseded or not
	// stored locally at all, e.g. a cached peer server's countersignature).
	createUserSignaturesTable := `
	CREATE TABLE IF NOT EXISTS user_signatures (
		id SERIAL PRIMARY KEY,
		public_key_id VARCHAR(255) NOT NULL,
		signature TEXT NOT NULL
	);`

	createServerSignaturesTable := `
	CREATE TABLE IF NOT EXISTS server_signatures (
		id SERIAL PRIMARY KEY,
		private_key_id VARCHAR(255) NOT NULL,
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

	// Server-owned private keys. Unaffected by the public_keys unification
	// below — private key material never leaves this table, regardless of
	// whose key it is (a user's private key never touches the server at all).
	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		id VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		revoked_at TIMESTAMP
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
		predecessor_id VARCHAR(255) REFERENCES public_keys(id),
		-- Bound by an admin rebind: only good for rotating away from itself.
		restricted BOOLEAN NOT NULL DEFAULT FALSE
	);`

	createPublicKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_public_keys_owner
		ON public_keys(owner) WHERE owner IS NOT NULL;
	`

	// A row's existence means the server key is revoked, in favour of its
	// successor. Both keys sign the revocation; compromised means the revoked
	// key is no longer trusted for anything signed after it.
	createPrivateKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS private_key_revocations (
		key_id VARCHAR(255) PRIMARY KEY REFERENCES private_keys(id) ON DELETE CASCADE,
		reason TEXT NOT NULL DEFAULT '',
		compromised BOOLEAN NOT NULL,
		successor VARCHAR(255) NOT NULL UNIQUE REFERENCES private_keys(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		successor_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

	// Two-admin requests. closed_by is the approver, the requester on a
	// withdrawal, or NULL when the server closes it as moot. A broadcast or
	// message has no reason: its text is in admin_request_content.
	createAdminRequestsTable := `
	CREATE TABLE IF NOT EXISTS admin_requests (
		id VARCHAR(255) PRIMARY KEY,
		action VARCHAR(32) NOT NULL
			CHECK (action IN ('suspend', 'reinstate', 'kick', 'promote', 'demote',
				'permissions', 'rebind', 'broadcast', 'unicast')),
		requester_id VARCHAR(255) NOT NULL REFERENCES identities(id),
		requester_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id),
		requester_signature_id INT NOT NULL REFERENCES user_signatures(id),
		requester_server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		reason TEXT CHECK (reason <> ''),
		created_at TIMESTAMP NOT NULL,
		state VARCHAR(16) NOT NULL DEFAULT 'pending'
			CHECK (state IN ('pending', 'executed', 'rejected', 'withdrawn', 'moot')),
		closed_by VARCHAR(255) REFERENCES identities(id),
		closer_key_id VARCHAR(255) REFERENCES public_keys(id),
		closer_signature_id INT REFERENCES user_signatures(id),
		server_signature_id INT REFERENCES server_signatures(id),
		closed_at TIMESTAMP,

		CONSTRAINT admin_requests_reason CHECK (
			(action IN ('broadcast', 'unicast')) = (reason IS NULL))
	);`

	// The armored key of a rebind, the text of a broadcast or unicast, or the
	// permission set of a promote or permissions change.
	createAdminRequestContentTable := `
	CREATE TABLE IF NOT EXISTS admin_request_content (
		request_id VARCHAR(255) PRIMARY KEY REFERENCES admin_requests(id) ON DELETE CASCADE,
		body TEXT NOT NULL
	);`

	// dropped marks a target whose account was removed while the request
	// was pending; a request whose targets are all dropped goes moot.
	createAdminRequestTargetsTable := `
	CREATE TABLE IF NOT EXISTS admin_request_targets (
		request_id VARCHAR(255) NOT NULL REFERENCES admin_requests(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
		dropped BOOLEAN NOT NULL DEFAULT FALSE,
		PRIMARY KEY (request_id, user_id)
	);`

	createAdminRequestTargetsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_admin_request_targets_user
		ON admin_request_targets(user_id) WHERE NOT dropped;
	`

	// users is a satellite of identities — profile fields only. id IS
	// identities.id directly. invite_id is the claimed invite's id, not FK'd
	// so it survives a rebuild without invites; invite_creator reads the inviter.
	createInviteCreatorFunction := `
	CREATE OR REPLACE FUNCTION invite_creator(invite_id TEXT) RETURNS TEXT AS $$
		SELECT left(invite_id, length(invite_id) - strpos(reverse(invite_id), '/'))
	$$ LANGUAGE sql IMMUTABLE;
	`

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		username VARCHAR(255) UNIQUE,
		role VARCHAR(16) NOT NULL DEFAULT 'user'
			CHECK (role IN ('root', 'admin', 'user')),
		bio TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		active_key_id VARCHAR(255) REFERENCES public_keys(id),
		user_signature_id INT REFERENCES user_signatures(id),
		server_signature_id INT REFERENCES server_signatures(id),
		invite_id VARCHAR(255),
		-- The request that suspended the user; NULL when not suspended.
		suspended_by VARCHAR(255) REFERENCES admin_requests(id)
	);`

	createUserIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_lower_users_username
		ON users(LOWER(username));
	`

	// An admin's permissions. Root holds every permission without rows here.
	createUserPermissionsTable := `
	CREATE TABLE IF NOT EXISTS user_permissions (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		permission VARCHAR(32) NOT NULL
			CHECK (permission IN ('admin.create', 'admin.demote', 'user.kick', 'user.suspend',
				'user.rebind', 'message.unicast', 'message.broadcast',
				'mesh.invite', 'mesh.connect', 'mesh.disconnect')),
		PRIMARY KEY (user_id, permission)
	);`

	// The permissions an admin invite grants to whoever claims it.
	createInvitePermissionsTable := `
	CREATE TABLE IF NOT EXISTS invite_permissions (
		invite_id VARCHAR(255) NOT NULL REFERENCES invites(id) ON DELETE CASCADE,
		permission VARCHAR(32) NOT NULL
			CHECK (permission IN ('admin.create', 'admin.demote', 'user.kick', 'user.suspend',
				'user.rebind', 'message.unicast', 'message.broadcast',
				'mesh.invite', 'mesh.connect', 'mesh.disconnect')),
		PRIMARY KEY (invite_id, permission)
	);`

	// A row's existence means the key is revoked, signed by the key's owner
	// or, for a lost device, by an admin request. successor_signature_id is
	// the OLD key's signature over its successor, so a rebind has none.
	createPublicKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS public_key_revocations (
		key_id VARCHAR(255) PRIMARY KEY
			REFERENCES public_keys(id) ON DELETE CASCADE,
		reason TEXT,
		user_signature_id INT REFERENCES user_signatures(id),
		admin_request_id VARCHAR(255) REFERENCES admin_requests(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		successor VARCHAR(255) REFERENCES public_keys(id),
		successor_signature_id INT REFERENCES user_signatures(id),

		CONSTRAINT public_key_revocations_signer CHECK (
			(admin_request_id IS NULL) = (user_signature_id IS NOT NULL))
	);`

	// reed_identities is to reeds what identities is to users: a thin
	// pointer row other tables reference whether or not this server holds
	// the reed's actual content (local, or foreign learned via relay).
	createReedIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS reed_identities (
		id VARCHAR(255) PRIMARY KEY,
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		-- Lets catch-up find foreign reeds, which have no reeds row here.
		author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createReedIdentitiesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_identities_server_id
		ON reed_identities(server_id);
	CREATE INDEX IF NOT EXISTS idx_reed_identities_author_id
		ON reed_identities(author_id);
	`

	// Tip reed metadata. Signature ids store the attestations so SignReed
	// retries can return the same countersignature (idempotent). reeds is
	// always the local-only satellite of reed_identities.
	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		signed_at TIMESTAMP NOT NULL,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		-- When the author's client could first serve it (PUBLISH_READY);
		-- NULL for reeds never published here, e.g. recovered ones.
		published_at TIMESTAMP,
		-- Set only on thread parts; thread_head matches reed_threads(id).
		thread_head VARCHAR(255) REFERENCES reed_identities(id) ON DELETE CASCADE,
		thread_index SMALLINT,
		CHECK ((thread_head IS NULL) = (thread_index IS NULL))
	);`

	// How far each peer has got through each local author's reeds and
	// removals. The row holds no event data: what is
	// still owed is derived from reeds and reed_removals past the cursor.
	createPeerAuthorCursorsTable := `
	CREATE TABLE IF NOT EXISTS peer_author_cursors (
		peer_server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		cursor_at TIMESTAMP NOT NULL,
		cursor_kind SMALLINT NOT NULL,
		cursor_reed_id VARCHAR(255) NOT NULL,
		claimed_at TIMESTAMP,
		PRIMARY KEY (peer_server_id, author_id)
	);`

	// Reed ids embed a time-ordered uuid after the author prefix, so id DESC
	// is chronological — but only within a single author, never across them.
	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id_id
		ON reeds(user_id, id DESC);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_reeds_thread
		ON reeds(thread_head, thread_index) WHERE thread_head IS NOT NULL;
	`

	// The author-signed thread record (index -> reed ID), keyed by the head.
	// Its parts are the reeds rows with thread_head = id.
	createReedThreadsTable := `
	CREATE TABLE IF NOT EXISTS reed_threads (
		id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

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

	// root_id is the conversation's root reed; one row per reply.
	// reed_id FKs to reed_identities, not reeds directly, since a reply's
	// home server may relay just a reference rather than the content itself.
	createReedRepliesTable := `
	CREATE TABLE IF NOT EXISTS reed_replies (
		root_id VARCHAR(255) NOT NULL,
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
		parent_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		timestamp TIMESTAMP NOT NULL
	);`

	createReedRepliesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_timestamp
		ON reed_replies (parent_reed_id, timestamp);

	CREATE INDEX IF NOT EXISTS idx_reed_replies_root
		ON reed_replies (root_id, timestamp);
	`

	// One row per (reed, mentioned user) — the mentioned user's pull inbox,
	// since the server never sees content to notify them directly.
	// mentioning_reed_id FKs reed_identities so a foreign reed can qualify.
	createReedMentionsTable := `
	CREATE TABLE IF NOT EXISTS reed_mentions (
		mentioning_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		mentioned_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (mentioning_reed_id, mentioned_user_id)
	);`

	createReedMentionsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_mentions_mentioned
		ON reed_mentions (mentioned_user_id, created_at);

	CREATE INDEX IF NOT EXISTS idx_reed_mentions_reed
		ON reed_mentions (mentioning_reed_id);
	`

	// One row per (reed, server) whose admins the reed mentions as @admins.
	// Admins learn of it through this server's row, never one row each.
	createReedAdminMentionsTable := `
	CREATE TABLE IF NOT EXISTS reed_admin_mentions (
		mentioning_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (mentioning_reed_id, server_id)
	);
	CREATE INDEX IF NOT EXISTS idx_reed_admin_mentions_server
		ON reed_admin_mentions (server_id, created_at);
	`

	// Signed thread-removal certificates, with the thread record they remove
	// (verified when it came from a peer) so it can be redelivered.
	createThreadRemovalsTable := `
	CREATE TABLE IF NOT EXISTS thread_removals (
		thread_id VARCHAR(255) PRIMARY KEY,
		public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		thread_record JSONB NOT NULL
	);`

	// Signed reed-removal certificates. Source of truth for "gone"; no FK to
	// reeds(id) so the live row may be dropped after the cert is stored.
	// A thread part's row names its thread removal and carries no signatures.
	createReedRemovalsTable := `
	CREATE TABLE IF NOT EXISTS reed_removals (
		reed_id VARCHAR(255) PRIMARY KEY,
		public_key_id VARCHAR(255) REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT REFERENCES user_signatures(id),
		server_signature_id INT REFERENCES server_signatures(id),
		thread_id VARCHAR(255) REFERENCES thread_removals(thread_id) ON DELETE CASCADE,
		CHECK ((thread_id IS NULL) = (public_key_id IS NOT NULL
			AND user_signature_id IS NOT NULL AND server_signature_id IS NOT NULL))
	);
	CREATE INDEX IF NOT EXISTS idx_reed_removals_thread
		ON reed_removals(thread_id) WHERE thread_id IS NOT NULL;`

	// Account-removal certificates, one per user: signed by the user, or for a
	// kick by two admins (our request, or a peer's admin_authorization). Public
	// keys remain. The API caps note at 140 characters, as a reader counts them.
	createAccountRemovalsTable := `
	CREATE TABLE IF NOT EXISTS account_removals (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		note TEXT NOT NULL DEFAULT '',
		public_key_id VARCHAR(255) REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT REFERENCES user_signatures(id),
		admin_request_id VARCHAR(255) REFERENCES admin_requests(id),
		admin_authorization BYTEA,
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		CONSTRAINT account_removals_signer CHECK (
			(CASE WHEN public_key_id IS NOT NULL AND user_signature_id IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN admin_request_id IS NOT NULL THEN 1 ELSE 0 END) +
			(CASE WHEN admin_authorization IS NOT NULL THEN 1 ELSE 0 END) = 1)
	);`

	// Encrypted server->user mailbox messages. No plaintext columns — the
	// row's mere existence is the undelivered-message record; it is
	// deleted once the client ACKs receipt.
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

	// Signed blocks. One table for blocks made here and blocks received
	// from peers against this server's users.
	createUserBlocksTable := `
	CREATE TABLE IF NOT EXISTS user_blocks (
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		blocked_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		PRIMARY KEY (user_id, blocked_user_id),
		CONSTRAINT user_blocks_not_self CHECK (user_id <> blocked_user_id)
	);`

	createUserBlocksIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_blocks_blocked_user
		ON user_blocks (blocked_user_id);`

	// The block or lift a foreign blocked user's home server is owed for
	// each pair, at most one, deleted once the peer accepts.
	createBlockPeerNoticesTable := `
	CREATE TABLE IF NOT EXISTS block_peer_notices (
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		blocked_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		kind VARCHAR(8) NOT NULL CHECK (kind IN ('block', 'unblock')),

		PRIMARY KEY (server_id, user_id, blocked_user_id)
	);`

	// A removal of one of our users each peer holding their content is owed,
	// deleted once the peer accepts.
	createAccountRemovalPeerNoticesTable := `
	CREATE TABLE IF NOT EXISTS account_removal_peer_notices (
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,

		PRIMARY KEY (server_id, user_id)
	);`

	// The block or lift a local blocked user's client is owed for each
	// pair, at most one, replaced by a later one and deleted on ack.
	createUserBlockEventsTable := `
	CREATE TABLE IF NOT EXISTS user_block_events (
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		blocked_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		kind VARCHAR(8) NOT NULL CHECK (kind IN ('block', 'unblock')),

		PRIMARY KEY (user_id, blocked_user_id)
	);
	CREATE INDEX IF NOT EXISTS idx_user_block_events_blocked
		ON user_block_events (blocked_user_id);`

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

	// Append-only history, one row per attestation. Withdrawing adds
	// signatures; re-vouching inserts a new row. No withdrawn_at: the
	// withdrawal countersignature carries the time.
	createUserVouchesTable := `
	CREATE TABLE IF NOT EXISTS user_vouches (
		id VARCHAR(255) PRIMARY KEY,
		voucher_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		voucher_key_id VARCHAR(255) NOT NULL,
		subject_user_id VARCHAR(255) NOT NULL,
		subject_key_id VARCHAR(255) NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		withdrawal_signature_id INT REFERENCES user_signatures(id),
		withdrawal_server_signature_id INT REFERENCES server_signatures(id)
	);`

	// One row per live attestation — what list reads hit. Uniqueness lives
	// here, where it holds: one live vouch per voucher per subject key.
	createUserVouchesActiveTable := `
	CREATE TABLE IF NOT EXISTS user_vouches_active (
		vouch_id VARCHAR(255) PRIMARY KEY REFERENCES user_vouches(id) ON DELETE CASCADE,
		voucher_user_id VARCHAR(255) NOT NULL,
		subject_user_id VARCHAR(255) NOT NULL,
		subject_key_id VARCHAR(255) NOT NULL,

		UNIQUE (voucher_user_id, subject_key_id)
	);`

	// Outbound is the direction path finding walks.
	createUserVouchesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_vouches_voucher_created
		ON user_vouches(voucher_user_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_user_vouches_subject_key
		ON user_vouches(voucher_user_id, subject_key_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_user_vouches_active_subject
		ON user_vouches_active(subject_user_id);
	CREATE INDEX IF NOT EXISTS idx_user_vouches_active_voucher
		ON user_vouches_active(voucher_user_id);
	`

	// Vouches made on a peer about this server's users. Only the id and the
	// voucher's server: the cert stays on that server and is read from there.
	createVouchReferencesTable := `
	CREATE TABLE IF NOT EXISTS vouch_references (
		vouch_id VARCHAR(255) PRIMARY KEY,
		subject_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		subject_key_id VARCHAR(255) NOT NULL,
		voucher_server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_vouch_references_subject
		ON vouch_references(subject_user_id);`

	createRipplesTable := `
	CREATE TABLE IF NOT EXISTS ripples (
		reed_id VARCHAR(255) PRIMARY KEY REFERENCES reeds(id) ON DELETE CASCADE,
		last_activity_at TIMESTAMP NOT NULL
	);`

	createRipplesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_ripples_last_activity
		ON ripples (last_activity_at);
	`

	createRippleResponsesTable := `
	CREATE TABLE IF NOT EXISTS ripple_responses (
		id VARCHAR(64) PRIMARY KEY,
		reed_id VARCHAR(255) NOT NULL REFERENCES ripples(reed_id) ON DELETE CASCADE,
		thread_id UUID NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		content TEXT NOT NULL,
		replying_to VARCHAR(64) REFERENCES ripple_responses(id) ON DELETE SET NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE,
		posted_at TIMESTAMP NOT NULL,

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

	createPinnedReedsTable := `
	CREATE TABLE IF NOT EXISTS pinned_reeds (
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		pinned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, reed_id)
	);`

	createPinnedReedsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pinned_reeds_reed_id
		ON pinned_reeds(reed_id);
	`

	// //////////// //
	//   Realtime   //
	// //////////// //

	// last_pong is the client-driven liveness heartbeat: the SPA sends PONG
	// every minute and the reaper evicts rows older than two. Without it a
	// crashed process leaves presence rows that look live forever.
	createOnlineUsersTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS online_users (
		user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
		sync_request_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_pong TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`

	createOnlineUsersIndexes := `
	CREATE INDEX IF NOT EXISTS idx_online_users_last_pong
		ON online_users(last_pong);
	`

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

	// The time a user is showing their verification code; a vouch for them
	// is accepted only then. Cascading off online_users closes it offline.
	createVerificationWindowsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS verification_windows (
		id UUID PRIMARY KEY,
		user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
		last_ping_at TIMESTAMP NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_verification_windows_user ON verification_windows(user_id);
	`

	// Pipe (hashtag) subscriptions. Cascading off online_users means a
	// disconnect clears them without any explicit teardown. tag is stored
	// already normalized by normalizePipeTag — never normalized in SQL.
	createPipeSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pipe_subscriptions (
		user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
		tag VARCHAR(255) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, tag)
	);`

	createPipeSubscriptionsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pipe_subscriptions_tag
		ON pipe_subscriptions(tag);
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
		IF TG_OP = 'INSERT' AND NEW.echoing_author_id != NEW.echoed_author_id THEN
			PERFORM bump_reed_stat(NEW.echoed_reed_id, 'echo_count', 1);
		ELSIF TG_OP = 'DELETE' AND OLD.echoing_author_id != OLD.echoed_author_id THEN
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
		requester_user_id VARCHAR(255) REFERENCES online_users(user_id) ON DELETE CASCADE,
		event_name VARCHAR(255) NOT NULL,
		subscription_id VARCHAR(255) REFERENCES profile_subscriptions(subscription_id) ON DELETE CASCADE,
		dispatched_at TIMESTAMP,
		-- Who the event was sent to; only they may answer a relay request.
		dispatched_to VARCHAR(255),
		-- Set when relayed reed content reached the requester; gates its DATA_ACK.
		relayed_at TIMESTAMP,
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

	// A key_revoked event's subject: the revoked user key, local or foreign.
	createPendingKeyEventsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_key_events (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE
	);`

	// The user keys each local user has cached, foreign ones included (cached
	// once verified). A revoked key still allocated is owed a KEY_REVOKED,
	// deleted on ack; revoked flags a foreign key revoked by its home's notice.
	createPublicKeyAllocationsTable := `
	CREATE TABLE IF NOT EXISTS public_key_allocations (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		revoked BOOLEAN NOT NULL DEFAULT FALSE,
		PRIMARY KEY (user_id, key_id)
	);
	CREATE INDEX IF NOT EXISTS idx_public_key_allocations_key
		ON public_key_allocations(key_id);`

	// Peers that fetched one of this server's user keys for their users, so
	// a revocation reaches each once. Deleted when the peer accepts it.
	createPublicKeyServerAllocationsTable := `
	CREATE TABLE IF NOT EXISTS public_key_server_allocations (
		server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (server_id, key_id)
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
	// The parts of a foreign thread relayed for event_id, from a thread
	// record this server verified; its DATA_ACK allocates each one.
	createPendingThreadPartsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS pending_thread_parts (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		reed_ids TEXT[] NOT NULL
	);`

	createForeignRelayRequestsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS foreign_relay_requests (
		event_id VARCHAR(255) PRIMARY KEY
			REFERENCES pending_events(event_id) ON DELETE CASCADE,
		requesting_server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		requesting_user_id VARCHAR(255) NOT NULL,
		-- The key the holder must encrypt to, as the requester's server named it.
		requesting_key_id VARCHAR(255) NOT NULL,
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
	// durably subscribe to a foreign reed's stats too. viewer_user_id FKs to
	// identities, not online_users: foreign viewers have no presence row here.
	createReedSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS reed_subscriptions (
		subscription_id VARCHAR(255) PRIMARY KEY,
		viewer_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (viewer_user_id, reed_id)
	);`

	createReedSubscriptionsIndex := `
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

	// Single-use claim challenges; the cleanup job deletes expired ones.
	createRecoveryChallengesTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS recovery_challenges (
		nonce VARCHAR(64) PRIMARY KEY,
		issued_at TIMESTAMP NOT NULL DEFAULT NOW()
	);`

	// Account recovery keeps its own challenges, apart from server recovery's.
	createAccountRecoveryChallengesTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS account_recovery_challenges (
		nonce VARCHAR(64) PRIMARY KEY,
		issued_at TIMESTAMP NOT NULL DEFAULT NOW()
	);`

	// WebSocket handshake nonces, kept until their timestamp leaves the window.
	createRealtimeAuthNoncesTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS realtime_auth_nonces (
		nonce VARCHAR(64) PRIMARY KEY,
		expires_at TIMESTAMPTZ NOT NULL
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

	// Reported records that name something not restored yet: an identity
	// whose keys verify them, or the ledger entry before one.
	createPendingRecoveryRecordsTable := `
	CREATE TABLE IF NOT EXISTS pending_recovery_records (
		waiting_on VARCHAR(255) NOT NULL,
		digest BYTEA NOT NULL,
		record BYTEA NOT NULL,
		PRIMARY KEY (waiting_on, digest)
	);`

	createPendingFollowsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_pending_follows_following_user_id
		ON pending_follows(following_user_id);
	`

	// Invites — operational redeem state plus the inviter's attestation.
	// id is canonical (creatorID@serverID/uuid) and globally unique, so it
	// alone is PK. user_signature_id is the inviter's signature over the invite.
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
			CHECK (granted_role IN ('admin', 'user')),
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		server_signature_id INT NOT NULL REFERENCES server_signatures(id)
	);`

	createInvitesIndexes := `
	CREATE INDEX IF NOT EXISTS idx_invites_created_by
		ON invites(created_by);
	`

	// The public ledger of admin actions, chained: each entry signs prev_id
	// and that entry's signature; prev_id is NULL only on the first entry.
	// A request or admin invite is logged once done: requester_id proposed
	// it, user_id approved or claimed it. A server key rotation has no user.
	createLedgerEntriesTable := `
	CREATE TABLE IF NOT EXISTS ledger_entries (
		id BIGSERIAL PRIMARY KEY,
		prev_id BIGINT UNIQUE REFERENCES ledger_entries(id),
		kind VARCHAR(16) NOT NULL
			CHECK (kind IN ('request', 'admin-invite', 'mesh', 'server-key')),
		user_id VARCHAR(255) REFERENCES identities(id),
		requester_id VARCHAR(255) REFERENCES identities(id),
		requested_at TIMESTAMP,
		message TEXT NOT NULL,
		-- A broadcast's text, or the permission set an entry grants.
		content TEXT,
		signed_at TIMESTAMP NOT NULL,
		server_signature_id INT NOT NULL REFERENCES server_signatures(id),

		CONSTRAINT ledger_entries_requested CHECK ((requester_id IS NULL) = (requested_at IS NULL)),
		CONSTRAINT ledger_entries_user CHECK ((user_id IS NULL) = (kind = 'server-key'))
	);`

	// The users an entry is about, so messages never carry usernames.
	createLedgerEntryTargetsTable := `
	CREATE TABLE IF NOT EXISTS ledger_entry_targets (
		entry_id BIGINT NOT NULL REFERENCES ledger_entries(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES identities(id),
		PRIMARY KEY (entry_id, user_id)
	);`

	// Only one entry can start the chain.
	createLedgerEntriesIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_first
		ON ledger_entries((prev_id IS NULL)) WHERE prev_id IS NULL;
	`

	// An admin closing a reed's open flags, with their reason. Not in the
	// ledger; a reed flagged again later gets a dismissal of its own.
	createFlagDismissalsTable := `
	CREATE TABLE IF NOT EXISTS flag_dismissals (
		id BIGSERIAL PRIMARY KEY,
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		dismissed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		reason TEXT NOT NULL,
		dismissed_at TIMESTAMP NOT NULL
	);`

	// Signed flags on reeds authored here; reporter_id may be a remote member.
	// author_id lets visibility be checked without the reed body. created_at
	// is the timestamp the reporter signed.
	createReedFlagsTable := `
	CREATE TABLE IF NOT EXISTS reed_flags (
		reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
		author_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		reporter_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
		note TEXT NOT NULL DEFAULT '',
		public_key_id VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
		user_signature_id INT NOT NULL REFERENCES user_signatures(id),
		created_at TIMESTAMP NOT NULL,
		dismissal_id BIGINT REFERENCES flag_dismissals(id) ON DELETE CASCADE,
		PRIMARY KEY (reed_id, reporter_id)
	);`

	createReedFlagsIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_flags_open
		ON reed_flags(created_at) WHERE dismissal_id IS NULL;
	CREATE INDEX IF NOT EXISTS idx_reed_flags_dismissal
		ON reed_flags(dismissal_id) WHERE dismissal_id IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_flag_dismissals_recent
		ON flag_dismissals(dismissed_at DESC, id DESC);
	`

	// federation_invitation lives while the handshake runs: new ->
	// accepted/canceled; approval deletes it. fingerprint/public_key_armor
	// are the peer's unverified key, promoted into public_keys on approval.
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
		status VARCHAR(16) NOT NULL DEFAULT 'new'
			CHECK (status IN ('new', 'accepted', 'canceled')),
		reviewed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
		reviewed_at TIMESTAMPTZ,
		connection_ciphertext TEXT
	);`

	// One row per handshake attempt against a peer, while it is pending or
	// once rejected. Approval deletes it, and its invitation, with their
	// logs: the server_mesh row takes over. invitation_id is set on the
	// INITIATOR side only.
	createFederationAttemptTable := `
	CREATE TABLE IF NOT EXISTS federation_attempt (
		id VARCHAR(255) PRIMARY KEY,
		remote_server_id VARCHAR(16) NOT NULL,
		remote_server_name VARCHAR(255) NOT NULL,
		base_url TEXT NOT NULL,
		frontend_url TEXT NOT NULL,
		fingerprint VARCHAR(255) NOT NULL,
		public_key_armor TEXT NOT NULL,
		invitation_id VARCHAR(255) REFERENCES federation_invitation(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		status VARCHAR(16) NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'rejected')),
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
		server_id VARCHAR(16) NOT NULL REFERENCES server_mesh(server_id) ON DELETE CASCADE,
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
		createServersIndexes,
		createServerMeshTable,
		createServerSelfTable,

		createUserSignaturesTable,
		createServerSignaturesTable,

		createIdentitiesTable,
		createIdentitiesIndexes,

		createPrivateKeysTable,

		createPublicKeysTable,
		createPublicKeyIndexes,
		createPrivateKeyRevocationsTable,

		createAdminRequestsTable,
		createAdminRequestContentTable,
		createAdminRequestTargetsTable,
		createAdminRequestTargetsIndexes,

		createInvitesTable,
		createInvitesIndexes,

		createInviteCreatorFunction,
		createUsersTable,
		createUserIndexes,
		createUserPermissionsTable,
		createInvitePermissionsTable,

		createPublicKeyRevocationsTable,

		createReedIdentitiesTable,
		createReedIdentitiesIndexes,

		createReedsTable,
		createReedIndexes,
		createReedThreadsTable,

		createReedEchoesTable,
		createReedEchoesIndexes,

		createReedRepliesTable,
		createReedRepliesIndexes,

		createReedMentionsTable,
		createReedMentionsIndexes,
		createReedAdminMentionsTable,

		createThreadRemovalsTable,
		createReedRemovalsTable,
		createAccountRemovalsTable,

		createPeerAuthorCursorsTable,

		createUserMailboxTable,
		createUserMailboxIndexes,

		createReedsLikedTable,
		createReedsLikedIndexes,

		createUserBlocksTable,
		createUserBlocksIndexes,
		createUserBlockEventsTable,
		createBlockPeerNoticesTable,
		createAccountRemovalPeerNoticesTable,

		createUserVouchesTable,
		createUserVouchesActiveTable,
		createUserVouchesIndexes,
		createVouchReferencesTable,

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

		createPinnedReedsTable,
		createPinnedReedsIndexes,

		// Realtime
		createOnlineUsersTable,
		createOnlineUsersIndexes,

		createBroadcastSubscriptionsTable,
		createBroadcastSubscriptionIndexes,

		createPipeSubscriptionsTable,
		createPipeSubscriptionsIndexes,
		createVerificationWindowsTable,

		createReedAllocationsTable,
		createReedAllocationIndexes,

		createReedServerAllocationsTable,
		createReedServerAllocationIndexes,

		createPendingFanoutTable,

		createNetworkStatsTable,
		seedNetworkStats,

		createReedStatsTable,
		createReedCoverageView,

		createProfileSubscriptionsTable,
		createProfileSubscriptionsIndex,

		createReedSubscriptionsTable,
		createReedSubscriptionsIndex,

		createPendingEventsTable,
		createPendingEventsIndexes,

		createPendingReedEventsTable,
		createPendingReedEventsIndexes,

		createPendingAccountEventsTable,
		createPendingKeyEventsTable,
		createPublicKeyAllocationsTable,
		createPublicKeyServerAllocationsTable,

		createForeignPendingEventsTable,
		createForeignPendingEventsIndexes,

		createForeignRelayRequestsTable,
		createForeignRelayRequestsIndexes,
		createPendingThreadPartsTable,

		// Triggers

		createBumpReedStatFunction,

		createBumpReplyAncestorsFunction,

		createAccountRemovalReplyCountTriggerFunction,
		createAccountRemovalReplyCountTrigger,

		createEchoCountTriggerFunction,
		createEchoCountTriggers,

		createHolderCountTriggerFunction,
		createHolderCountTriggers,

		createLikeCountTriggerFunction,
		createLikeCountTriggers,

		createReedRemovalReplyCountTriggerFunction,
		createReedRemovalReplyCountTrigger,

		createReedReplyDeleteTriggerFunction,
		createReedReplyDeleteTrigger,

		createReplyCountTriggerFunction,
		createReplyCountTrigger,

		// Recovery
		createUnclaimedAccountsTable,

		createOngoingRecoveriesTable,

		createRecoveryChallengesTable,
		createAccountRecoveryChallengesTable,
		createRealtimeAuthNoncesTable,

		createPendingFollowsTable,
		createPendingFollowsIndexes,
		createPendingRecoveryRecordsTable,

		// Federation
		createFederationLogTable,

		createFederationInvitationTable,
		createFederationInvitationLogTable,

		createFederationAttemptTable,
		createFederationAttemptLogTable,

		createFederationServerLogTable,

		// Admin
		createLedgerEntriesTable,
		createLedgerEntriesIndexes,
		createLedgerEntryTargetsTable,
		createFlagDismissalsTable,
		createReedFlagsTable,
		createReedFlagsIndexes,
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
