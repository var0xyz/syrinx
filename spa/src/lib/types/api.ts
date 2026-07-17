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
  server: ServerSignature;
  hasReeds: boolean;
};

// ServerSignature is the server's countersignature metadata shared by every
// signed resource (identity records, public keys, reeds, …). Clients
// pass it to `verify(server, payload)` after rebuilding the resource's
// canonical payload.
export interface ServerSignature extends Base {
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

// KeyPredecessor is the rotation handoff proof on keys uploaded via
// AddPublicKey: predecessor fingerprint + detached signature over armor.
export interface KeyPredecessor extends Base {
  fingerprint: string;
  signature: string;
};

// PublicKey is the wire shape of a distributed user public key.
// `server` is required (countersignature over userID/fingerprint/armor).
// `revoked` is computed on read — revocation details live in KeyRevocation.
// `predecessor` is null for signup keys; set for rotation keys.
export interface PublicKey extends Base {
  fingerprint: string;
  userID: string;
  armor: string;
  createdAt?: string;
  expiresAt?: string | null;
  identities?: PublicKeyIdentity[];
  revoked: boolean;
  predecessor: KeyPredecessor | null;
  server: ServerSignature;
};

// KeyRevocation is the wire shape of a signed revocation attestation.
// Revoke time is server.timestamp. successor is bookkeeping written
// later by AddPublicKey and is not covered by either signature.
export interface KeyRevocation extends Base {
  fingerprint: string;
  userID: string;
  reason: string;
  successor: string | null;
  signature: string;
  server: ServerSignature;
};
