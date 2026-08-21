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
