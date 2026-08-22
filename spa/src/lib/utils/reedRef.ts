import { parseKeyId, formatKeyId } from './keyId';

/**
 * Reed reference: userID@serverID/reedID (echoing / replying), where
 * userID is already the canonical form. authorId below is that whole
 * canonical userID (bare@serverId), not just the bare local part — every
 * consumer (getReed, reed.userID comparisons, …) needs the canonical form.
 * Same 3-part shape as a key id, so this is a thin wrapper over
 * parseKeyId/formatKeyId (see keyId.ts).
 */
export type ReedRef = {
  authorId: string;
  serverId: string;
  reedId: string;
};

export function parseReedRef(raw: string | null | undefined): ReedRef | null {
  const parsed = parseKeyId(raw);
  if (!parsed) return null;
  return { authorId: parsed.userId, serverId: parsed.serverId, reedId: parsed.fingerprint };
}

export function formatReedRef(authorId: string, serverId: string, reedId: string): string {
  return formatKeyId(authorId, serverId, reedId);
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
 * A reed's own canonical id (authorID@serverID/uuid) — same composition
 * the server uses for canonicalReedID (handlers.go), and the value every
 * downstream consumer (ripples, likes, IndexedDB keys, realtime
 * subscriptions) should use to refer to this reed. reed.id itself stays
 * the bare client-minted UUID — required as-is for the signed markdown
 * envelope and the /reeds/{userID}/{reedID} URL segment.
 */
export function canonicalReedId(reed: { userID: string; id: string }): string {
  return refForReed(reed.userID, reed.id);
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
  parent: { id: string; threadId?: string },
): string {
  if (parent.threadId) return parent.threadId;
  return parent.id;
}
