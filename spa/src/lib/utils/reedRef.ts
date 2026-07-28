/** Reed reference: userID@serverID/reedID (echoing / replying). */
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
  const slash = trimmed.indexOf('/', at + 1);
  if (slash <= at + 1 || slash === trimmed.length - 1) return null;
  const authorId = trimmed.slice(0, at).trim();
  const serverId = trimmed.slice(at + 1, slash).trim();
  const reedId = trimmed.slice(slash + 1).trim();
  if (!authorId || !serverId || !reedId) return null;
  return { authorId, serverId, reedId };
}

export function formatReedRef(authorId: string, serverId: string, reedId: string): string {
  return `${authorId}@${serverId}/${reedId}`;
}
