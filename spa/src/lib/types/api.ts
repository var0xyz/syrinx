export type Base = {};

// User is the wire shape of a signed identity record. User-authored
// fields live at the root; the server's countersignature and its
// metadata live under `server`. `signature` (at the root) is the base64
// of the user's detached PGP signature over the canonical user identity
// payload (see buildUserIdentityPayload in services/signing.ts).
// `fingerprint` identifies the key that produced `signature` — it is
// self-describing per record, not a pointer to the user's "current" key.
export interface User extends Base {
  id: string;
  username: string;
  memberSince: string;
  avatarURL: string;
  bio: string;
  fingerprint: string;
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
  revoked?: { reason: string; timestamp: string } | null;
};