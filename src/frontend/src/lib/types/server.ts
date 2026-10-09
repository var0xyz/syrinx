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

/** An established peer, as /server/info lists it. `frontendUrl` is the origin its
 * users open links on. */
export type { FederatedServerInfo as FederatedServer } from '$lib/proto/identity_pb';

/** A server a verification link can open on. */
export interface VouchServerChoice {
  name: string;
  origin: string;
  isSelf: boolean;
}
