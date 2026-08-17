/**
 * Canonical id: entityId@serverId[/subEntityId], mirroring identity.CanonicalID
 * / identity.ParseIdentityID / identity.ParseKeyFingerprint on the Go side
 * (identity/identity_id.go). Splits on the LAST "@" and the LAST "/" — the
 * id alphabet never contains either character, so this is unambiguous even
 * if entityId/serverId/subEntityId themselves happened to contain one.
 *
 * Two shapes share this parser: a user key ("userID@serverID/fingerprint",
 * entityId = userID, subEntityId = fingerprint) and a server's own key
 * ("fingerprint@serverID", entityId = fingerprint, subEntityId absent).
 */
/** [entityId, serverId, subEntityId] — subEntityId is null for a 2-part id. */
export type CanonicalIdParts = [entityId: string, serverId: string, subEntityId: string | null];

export function parseCanonicalId(raw: string | null | undefined): CanonicalIdParts | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;

  const slash = trimmed.lastIndexOf('/');
  const atSearchEnd = slash >= 0 ? slash : trimmed.length;
  const at = trimmed.lastIndexOf('@', atSearchEnd - 1);
  if (at <= 0 || at === atSearchEnd - 1) return null;

  const entityId = trimmed.slice(0, at).trim();
  if (!entityId) return null;

  if (slash < 0) {
    const serverId = trimmed.slice(at + 1).trim();
    if (!serverId) return null;
    return [entityId, serverId, null];
  }

  if (slash === trimmed.length - 1) return null;
  const serverId = trimmed.slice(at + 1, slash).trim();
  const subEntityId = trimmed.slice(slash + 1).trim();
  if (!serverId || !subEntityId) return null;
  return [entityId, serverId, subEntityId];
}

/** Parses the 3-part user-key shape specifically — null if subEntityId is absent. */
export function parseKeyId(
  raw: string | null | undefined
): { userId: string; serverId: string; fingerprint: string } | null {
  const parsed = parseCanonicalId(raw);
  if (!parsed || parsed[2] === null) return null;
  const [userId, serverId, fingerprint] = parsed;
  return { userId, serverId, fingerprint };
}

export function formatKeyId(userId: string, serverId: string, fingerprint: string): string {
  return `${userId}@${serverId}/${fingerprint}`;
}

/**
 * Build a canonical key id from a userID that's already canonical
 * (userID@serverID) — e.g. the signed-in user's own id. Just appends the
 * bare fingerprint; does not touch serverId, because it's already embedded
 * in canonicalUserId.
 */
export function appendFingerprint(canonicalUserId: string, fingerprint: string): string {
  return `${canonicalUserId}/${fingerprint}`;
}

/**
 * Canonical id for a server's own signing key: "fingerprint@serverID" — no
 * owning user, unlike a user key's "userID@serverID/fingerprint". Mirrors
 * identity.CanonicalID(serverID, fingerprint) called with a key fingerprint
 * standing in for userID on the Go side (see handlers.go's GetKey doc).
 */
export function formatServerKeyId(fingerprint: string, serverId: string): string {
  return `${fingerprint}@${serverId}`;
}

/** Root is userId '1' on any server. */
export function isRoot(userID: string | null | undefined): boolean {
  const parsed = parseCanonicalId(userID);
  return !!parsed && parsed[2] === null && parsed[0] === '1';
}

/** Whether raw is a well-formed reed ref (userID@serverID/reedID). */
export function isValidRef(raw: string | null | undefined): boolean {
  return !!parseKeyId(raw);
}

/** The author's canonical userID (userID@serverID) embedded in a reed ref. */
export function getUserId(reedRef: string | null | undefined): string | null {
  const parsed = parseKeyId(reedRef);
  return parsed ? `${parsed.userId}@${parsed.serverId}` : null;
}

/** Whether two reed refs share the same author — callers never parse
 * either ref themselves. */
export function userMatches(a: string | null | undefined, b: string | null | undefined): boolean {
  const authorA = getUserId(a);
  return !!authorA && authorA === getUserId(b);
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
