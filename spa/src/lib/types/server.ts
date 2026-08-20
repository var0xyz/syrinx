export type SignupMode = 'open' | 'invite' | 'closed';

export interface ServerInfo {
  id: string;
  name: string;
  recoveryMode: boolean;
  signupMode: SignupMode;
  /** -1 means unlimited. */
  maxInvitesPerUser: number;
  serverKeyFingerprint: string;
}
