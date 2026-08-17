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
