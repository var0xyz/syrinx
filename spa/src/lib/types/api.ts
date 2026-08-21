export type Base = {};

export interface UserSignature extends Base {
  fingerprint: string;
  armor: string;
}

/** Nested server countersignature wire block shared by every signed resource. */
export interface ServerSignature extends Base {
  serverID: string;
  fingerprint: string;
  armor: string;
  timestamp: string;
}

// User is the wire shape of a signed identity record
// (GET /users/{id}/profile). User-authored fields live at the root;
// attestations nest under `userSignature` and `serverSignature`.
//
// Mutable / unsigned hints live on UserInfo (GET /users/{id}/info).
export interface InvitedBy {
  id: string;
  username: string;
}

export interface User extends Base {
  id: string;
  username: string;
  /** Server-assigned role, bound by serverSignature. */
  role: 'root' | 'admin' | 'user';
  memberSince: string;
  bio: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
  invitedBy: InvitedBy | null;
}

/** Unsigned hints + profile cache invalidation (GET /users/{id}/info). */
export interface UserInfo extends Base {
  id: string;
  hasReeds: boolean;
  followersCount: number;
  followingCount: number;
  activeKeyFingerprint: string;
  /** Same instant as the user's profile serverSignature.timestamp. */
  profileTimestamp: string;
}

// KeyPredecessor is a plain forward-pointer to the key this one replaced —
// no signature here; the predecessor's *own* revocation row carries the
// proof it approved the rotation (KeyRevocation.successorSignature).
export interface KeyPredecessor extends Base {
  id: string;
};

// PublicKey is the wire shape of a distributed user or server public key.
// `serverSignature` is required (countersignature over userID/id/armor).
// `revoked` is computed on read — revocation details live in KeyRevocation.
// `predecessor` is null for signup keys; set for rotation keys.
export interface PublicKey extends Base {
  id: string;
  userID: string;
  armor: string;
  createdAt?: string;
  revoked: boolean;
  predecessor: KeyPredecessor | null;
  serverSignature: ServerSignature;
};

// KeyRevocation is the wire shape of a signed revocation attestation.
// id is the revoked key's own id. Revoke time is serverSignature.timestamp.
// successor/successorSignature are bookkeeping written later by
// AddPublicKey (proof the *old* key approved the new one) and are not
// covered by either of this cert's own signatures.
export interface KeyRevocation extends Base {
  id: string;
  userID: string;
  reason: string;
  successor: string | null;
  successorSignature: string | null;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
};

// RecoveryKeyNode is one level of the nested key chain for recovery claim /
// peer identity POST bodies (matches recovery.KeyNode). Outermost is the
// active key; predecessor walks back to the signup key (null). `signature`
// is set only on predecessor links: the older key's detached sig over the
// newer (parent) key's armor.
//
// This bundle format is deliberately bare-everything (recovery/wire.go's
// signed-payload exception — see recovery/nest.go) and predates the
// /keys unification, so it keeps its own `fingerprint`-named revocation
// shape rather than reusing KeyRevocation.
export interface RecoveryKeyRevocation extends Base {
  fingerprint: string;
  userID: string;
  reason: string;
  successor: string | null;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
};

export interface RecoveryKeyNode extends Base {
  fingerprint: string;
  userID: string;
  armor: string;
  createdAt?: string;
  expiresAt?: string | null;
  revoked: boolean;
  serverSignature: ServerSignature;
  signature?: string;
  revocation: RecoveryKeyRevocation | null;
  predecessor: RecoveryKeyNode | null;
};

export type AccountRecoveryChallenge = {
  challenge: number;
};

export type AccountRecoveryBootstrapRequest = {
  challenge: number;
  userID: string;
  fingerprint: string;
  signature: string;
};

export type AccountRecoveryBootstrapResponse = {
  profile: User;
  following: string[];
  tipReedID: string | null;
  reedIDs: string[];
};

export type IdentityClaimChallenge = {
  challenge: number;
};

export type IdentityClaimRequest = {
  challenge: number;
  signature: string;
  profile: User;
  key: RecoveryKeyNode;
};

/** Authenticated POST /recovery/identity body (one peer, no challenge). */
export type PeerIdentityRequest = {
  profile: User;
  key: RecoveryKeyNode;
};

/** Authenticated POST /recovery/reeds body (one reed). */
export type RecoveryReedRequest = {
  reedID: string;
  authorID: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
};

/** Authenticated POST /recovery/following body (≤100 user IDs). */
export type RecoveryFollowingRequest = {
  userIDs: string[];
};

/** Wire shape of a signed reed removal certificate (DELETE /reeds response / 410 body). */
export interface ReedRemoval extends Base {
  type: 'reed';
  serverID: string;
  userID: string;
  reedID: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}

/** Wire shape of a signed account removal certificate (DELETE /users/me / 410 body). */
export interface AccountRemoval extends Base {
  type: 'account';
  serverID: string;
  userID: string;
  note: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}

/** Wire shape of a signed reed-like certificate (POST /reeds/{userID}/{reedID}/like). */
export interface ReedLike extends Base {
  serverID: string;
  authorID: string;
  reedID: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}

/** One direct reply in GET /reeds/{userID}/{reedID}/replies. */
export interface ReplyMeta extends Base {
  userID: string;
  reedID: string;
}

export interface ReplyListResponse extends Base {
  replies: ReplyMeta[];
  hasMore: boolean;
}

/**
 * A ripple response — POST/GET /reeds/{userID}/{reedID}/ripples.
 * `hash` is the id (content-addressed hex-SHA256 of the signed server
 * payload) — frozen at creation, never recomputed even by a soft delete.
 * See specs/ripples/00_design.md's Signing section.
 */
export interface Ripple extends Base {
  hash: string;
  threadID: string;
  userID: string;
  content: string;
  replyingTo: string | null;
  deleted: boolean;
  postedAt: string;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}

export interface RippleListResponse extends Base {
  responses: Ripple[];
  hasMore: boolean;
  nextCursor?: string;
  /** Absolute instant the whole ripples section on this reed disappears.
   * Converted to a local countdown once at fetch time (see
   * RipplesSection.svelte) rather than compared against the wall clock
   * on every tick, so the animation stays smooth even if the system
   * clock changes mid-session — but the value itself is independently
   * re-checkable against any fresh fetch, unlike a relative duration. */
  expiresAt?: string;
}

/** One row in GET /users/{userID}/following or /users/{userID}/followers. */
export interface FollowListUser extends Base {
  userID: string;
  followedAt: string;
}

export interface FollowListResponse extends Base {
  users: FollowListUser[];
  hasMore: boolean;
}

/** One row in GET /reeds/{userID}/{reedID}/chorus. */
export interface EchoerListUser extends Base {
  userID: string;
  echoedAt: string;
}

export interface EchoerListResponse extends Base {
  users: EchoerListUser[];
  hasMore: boolean;
}

/** Local + create-response shape for a signed invite (status is unsigned). */
export interface Invite extends Base {
  id: string;
  /** SHA-256(secret) hex — signed and stored on the server. */
  tokenHash: string;
  /** Role granted to the redeemer (user | admin). Omitted on wire when user. */
  grantedRole?: 'user' | 'admin';
  /** Fragment secret; local-only while pending. Never sent on create. */
  secret?: string;
  createdAt: string;
  status: 'pending' | 'claimed' | 'revoked';
  claimedAt?: string | null;
  claimedBy?: string | null;
  revokedAt?: string | null;
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}

export interface FederationInvitation {
  inviteId: string;
  name: string;
  status: 'new' | 'accepted' | 'approved' | 'rejected' | 'canceled' | 'revoked';
  createdBy: string;
  createdByUsername: string;
  remoteFingerprint: string;
  createdAt: string;
  acceptedAt?: string | null;
  serverId?: string | null;
  reviewedBy?: string | null;
  reviewedByUsername?: string | null;
  reviewedAt?: string | null;
  connectionString?: string;
}

export interface FederationInvitationCreateResponse {
  inviteId: string;
  connectionString: string;
  status: 'new';
}

export interface FederationAttemptResponse {
  status: 'accepted';
  serverId: string;
}

/** An approved peer — servers rows only exist once a FederationAttempt has
 * been approved (see ApproveFederationAttempt). */
export interface FederationServer {
  serverId: string;
  name: string;
  baseUrl: string;
  connected: boolean;
  createdAt: string;
}

/** A handshake attempt against a peer, at any stage — permanent audit
 * trail, never deleted. RemoteServerId/RemoteServerName/BaseUrl/Fingerprint
 * are the peer's own claims from its handshake payload. InvitationId is
 * set on the initiator side only; ServerId once approved. */
export interface FederationAttempt {
  attemptId: string;
  remoteServerId: string;
  remoteServerName: string;
  baseUrl: string;
  fingerprint: string;
  invitationId?: string | null;
  serverId?: string | null;
  createdAt: string;
  status: 'pending' | 'approved' | 'rejected';
  approvedBy?: string | null;
  approvedByUsername?: string | null;
  approvedAt?: string | null;
  rejectedBy?: string | null;
  rejectedByUsername?: string | null;
  rejectedAt?: string | null;
  rejectedReason?: string | null;
}

/** The mesh tab's combined view: invitations + attempts + servers. */
export interface FederationList {
  invitations: FederationInvitation[];
  attempts: FederationAttempt[];
  servers: FederationServer[];
}
