export type Base = {};

// User is the wire shape of a signed identity record. User-authored
// fields live at the root; the server's countersignature and its
// metadata live under `server`. `signature` (at the root) is the base64
// of the user's detached PGP signature over the canonical user identity
// payload (see buildUserIdentityPayload in services/signing.ts).
// `signatureFingerprint` identifies the key that produced `signature` —
// it is self-describing per record, not a pointer to the user's
// "current" key. Note: inside the canonical signed payload this header
// is still spelled `fingerprint` (that's the canonical byte sequence);
// the JSON field is a wire-only alias and does not affect signature
// verification.
//
// `activeKeyFingerprint` is a server-provided hint carrying the user's
// currently-active key fingerprint at response time. It is **not** part
// of the signed payload: the identity record is frozen at signing time,
// while the active key can change (rotation) without a new identity
// record. Clients compare `signatureFingerprint` (the record's signer)
// with `activeKeyFingerprint` (server's current view) — if they differ,
// the signer has been rotated and the client should re-fetch the
// record's signing key to learn its revocation state and follow the
// `successor` chain to reach the active one.
export interface User extends Base {
  id: string;
  username: string;
  memberSince: string;
  avatarURL: string;
  bio: string;
  signatureFingerprint: string;
  activeKeyFingerprint: string;
  signature: string;
  server: ServerBlock;
  hasReeds: boolean;
};

// ServerBlock is the server's contribution to an identity record: which
// server key countersigned it, when, and the signature itself.
export interface ServerBlock extends Base {
  id: string;
  fingerprint: string;
  timestamp: string;
  algorithm: string;
  signature: string;
};

export interface PublicKeyIdentity extends Base {
  id: string;
  value: string;
};

export interface PublicKey extends Base {
  fingerprint: string;
  userID: string;
  armor: string;
  name?: string;
  createdAt?: string;
  expiresAt?: string | null;
  identities?: PublicKeyIdentity[];
  revoked?: {
    reason: string;
    timestamp: string;
    // `successor` is the fingerprint of the key that replaced this one.
    // A client that pulls a revoked key can walk the chain by fetching
    // this successor recursively until it reaches a non-revoked key —
    // which will be the user's currently-active key. Null in the
    // transient window between RevokeKey and AddPublicKey.
    successor: string | null;
  } | null;
};