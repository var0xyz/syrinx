/** What a contradiction check needs from a vouch. */
export interface ContradictableVouch {
  subjectKeyID: string;
  withdrawn?: boolean;
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
  const live = vouches.filter((v) => !v.withdrawn);
  if (live.length === 0) return null;
  if (live.some((v) => v.subjectKeyID === claimedKeyID)) return null;
  return live[0];
}
