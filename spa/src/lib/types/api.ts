export type Base = {};

export interface User extends Base {
  id: string;
  username: string;
  memberSince: string;
  avatarURL: string;
  bio: string;
  fingerprint: string;
  server: string;
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
};