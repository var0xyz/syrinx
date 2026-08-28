import { parseKeyId, parseCanonicalId } from './keyId';

/** Parses a canonical userID@serverID — null for the 3-part key shape
 * (use parseKeyId) or a non-canonical string. */
export function parseUserRef(raw: string | null | undefined): { userId: string; serverId: string } | null {
  const parsed = parseCanonicalId(raw);
  if (!parsed || parsed[2] !== null) return null;
  const [userId, serverId] = parsed;
  return { userId, serverId };
}

/**
 * Reed reference: userID@serverID/reedID (echoing / replying), where
 * userID is already the canonical form. authorId below is that whole
 * canonical userID (bare@serverId), not just the bare local part — every
 * consumer (getReed, reed.userID comparisons, …) needs the canonical form.
 * Same 3-part shape as a key id, so this is a thin wrapper over
 * parseKeyId (see keyId.ts).
 */
export type ReedRef = {
  authorId: string;
  serverId: string;
  reedId: string;
};

export function parseReedRef(raw: string | null | undefined): ReedRef | null {
  const parsed = parseKeyId(raw);
  if (!parsed) return null;
  return {
    authorId: `${parsed.userId}@${parsed.serverId}`,
    serverId: parsed.serverId,
    reedId: parsed.fingerprint,
  };
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
  return `${reed.userID}/${reed.id}`;
}

/** Thread id for a reply: inherit parent's threadId or parent ref when parent is the root. */
export function resolveThreadId(
  parent: { id: string; threadId?: string },
): string {
  if (parent.threadId) return parent.threadId;
  return parent.id;
}
