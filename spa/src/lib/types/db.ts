import type * as backend from './api';

interface Metadata {
  createdAt: string;
  updatedAt: string;
}

export interface Base {}

export interface User extends Base, backend.User {
  __meta__: Metadata;
}

export interface PublicKeyIdentity extends Base, backend.PublicKeyIdentity {
  __meta__: Metadata;
}

export interface PublicKey extends Base, backend.PublicKey {
  __meta__: Metadata;
}