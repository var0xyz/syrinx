/** What a contradiction check needs from a vouch. */
export interface ContradictableVouch {
  subjectKeyId: string;
}

/**
 * Finds a live vouch naming a key other than the claimed one. Returns null
 * when no vouch exists: absence is not a contradiction, and refusing on it
 * would break relay for everyone who has verified nobody.
 */
export function findContradiction<T extends ContradictableVouch>(
  vouches: T[],
  claimedKeyID: string
): T | null {
  const live = vouches;
  if (live.length === 0) return null;
  if (live.some((v) => v.subjectKeyId === claimedKeyID)) return null;
  return live[0];
}
