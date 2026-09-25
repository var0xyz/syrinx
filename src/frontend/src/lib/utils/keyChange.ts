/** How a subject's key came to differ from the one a vouch names. */
export type KeyChangeKind = 'rotation' | 'unexplained' | 'revocation';

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
 * Classifies a key change so the UI can say which of three things
 * happened. A rotation is only "legitimate" when the new key proves the
 * old one approved it; without that proof it is the H1 alarm.
 */
export function classifyKeyChange(inputs: KeyChangeInputs): KeyChangeKind | null {
  if (inputs.vouchedKeyID === inputs.currentKeyID) return null;
  if (inputs.predecessorID === inputs.vouchedKeyID && inputs.handoffValid) {
    return 'rotation';
  }
  if (inputs.vouchedKeyRevoked) return 'revocation';
  return 'unexplained';
}
