export type SignupMode = 'open' | 'invite' | 'closed';

export interface ServerInfo {
  id: string;
  name: string;
  recoveryMode: boolean;
  signupMode: SignupMode;
  /** -1 means unlimited. */
  maxInvitesPerUser: number;
  /** This server's own current signing key's canonical id (fingerprint@serverID). */
  serverKeyId: string;
}

/** An established peer, as /server/info lists it. `baseUrl` is the origin its
 * users open links on. */
export interface FederatedServer {
  id: string;
  name: string;
  keyId: string;
  createdAt: string;
  baseUrl: string;
}

/** A server a verification link can open on. */
export interface VouchServerChoice {
  name: string;
  origin: string;
  isSelf: boolean;
}
