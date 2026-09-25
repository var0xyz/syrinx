/**
 * How a subject's key relates to the one a vouch names. Only `unexplained`
 * is an alarm; the rest describe a verification that no longer protects
 * anything, which is information rather than a warning.
 */
export type KeyChangeKind = 'rotation' | 'unexplained' | 'revocation' | 'vouched-key-revoked';

export interface KeyChangeInputs {
  /** The key a held vouch attests. */
  vouchedKeyID: string;
  /** The key the server now reports as active. */
  currentKeyID: string;
  /** The current key's predecessor id, when it declares one. */
  predecessorID?: string | null;
  /** Whether the predecessor's handoff signature verified. */
  handoffValid: boolean;
  /** Whether the vouched key carries its own revocation. */
  vouchedKeyRevoked: boolean;
}

/**
 * A rotation is only legitimate when the new key proves the old one
 * approved it; without that proof it is the H1 alarm. A matching id is not
 * automatically fine either — see 'vouched-key-revoked'.
 */
export function classifyKeyChange(inputs: KeyChangeInputs): KeyChangeKind | null {
  if (inputs.vouchedKeyID === inputs.currentKeyID) {
    return inputs.vouchedKeyRevoked ? 'vouched-key-revoked' : null;
  }
  if (inputs.predecessorID === inputs.vouchedKeyID && inputs.handoffValid) {
    return 'rotation';
  }
  if (inputs.vouchedKeyRevoked) return 'revocation';
  return 'unexplained';
}

/** Whether this state is the substitution alarm, shown on the profile. */
export function isKeyChangeAlarm(kind: KeyChangeKind | null): boolean {
  return kind === 'unexplained';
}
