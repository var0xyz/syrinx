/** Ordered by distance from the viewer; blue beats green beats grey. */
export type TrustMark = 'blue' | 'green' | 'grey' | 'none';

/** What a mark needs from a vouch, so the ladder stays independent of storage. */
export interface MarkableVouch {
  voucherUserID: string;
  subjectKeyID: string;
  /** Present once retracted; the viewer's own withdrawn vouches are kept. */
  withdrawal?: unknown;
}

/**
 * A vouch counts only while it is live and still describes the subject's
 * current key. Withdrawal is the only retraction, so a revoked voucher key
 * changes nothing here.
 */
export function countsForMark(vouch: MarkableVouch, activeKeyID: string): boolean {
  if (vouch.withdrawal) return false;
  return vouch.subjectKeyID === activeKeyID;
}

/**
 * The ladder. A stale vouch colours nothing, or a substituted key would
 * inherit the mark that exists to catch it.
 */
export function trustMarkFrom(
  vouches: MarkableVouch[],
  activeKeyID: string,
  me: string | null,
  activeRootIDs: Set<string>
): TrustMark {
  const live = vouches.filter((v) => countsForMark(v, activeKeyID));
  if (live.length === 0) return 'none';
  if (me && live.some((v) => v.voucherUserID === me)) return 'blue';
  if (live.some((v) => activeRootIDs.has(v.voucherUserID))) return 'green';
  return 'grey';
}
