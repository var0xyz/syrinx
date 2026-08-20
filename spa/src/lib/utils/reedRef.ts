/**
 * Reed reference: userID@serverID/reedID (echoing / replying), where
 * userID is already the canonical form. authorId below is that whole
 * canonical userID (bare@serverId), not just the bare local part — every
 * consumer (getReed, reed.userID comparisons, …) needs the canonical form.
 */
export type ReedRef = {
  authorId: string;
  serverId: string;
  reedId: string;
};

export function parseReedRef(raw: string | null | undefined): ReedRef | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const at = trimmed.indexOf('@');
  if (at <= 0) return null;
  const slash = trimmed.lastIndexOf('/');
  if (slash <= at || slash === trimmed.length - 1) return null;
  const authorId = trimmed.slice(0, slash).trim();
  const serverId = trimmed.slice(at + 1, slash).trim();
  const reedId = trimmed.slice(slash + 1).trim();
  if (!authorId || !serverId || !reedId) return null;
  return { authorId, serverId, reedId };
}

export function formatReedRef(authorId: string, serverId: string, reedId: string): string {
  return `${authorId}@${serverId}/${reedId}`;
}

/**
 * Build a ref from a userID that's already canonical (userID@serverID) —
 * e.g. reed.userID. Just appends the reedId; does not touch serverId,
 * because it's already embedded in userID.
 */
export function refForReed(userID: string, reedId: string): string {
  return `${userID}/${reedId}`;
}

/**
 * Re-key a canonical userID under a different serverId. Only needed for a
 * removed reed/account: the removal cert's serverId is authoritative once
 * the account is gone, and may not match the serverId already embedded in
 * userID (e.g. stale local cache). Not for the general case — a live
 * reed's own userID already has the right serverId; use refForReed there.
 */
export function refForRemoved(userID: string, serverId: string, reedId: string): string {
  const at = userID.indexOf('@');
  const authorId = at <= 0 ? userID : userID.slice(0, at);
  return formatReedRef(authorId, serverId, reedId);
}

/** Thread id for a reply: inherit parent's threadId or parent ref when parent is the root. */
export function resolveThreadId(
  parent: { userID: string; id: string; threadId?: string },
): string {
  if (parent.threadId) return parent.threadId;
  return refForReed(parent.userID, parent.id);
}
