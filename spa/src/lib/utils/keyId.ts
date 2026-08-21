/**
 * Canonical key id: userID@serverID/fingerprint, mirroring identity.CanonicalID
 * / identity.ParseKeyFingerprint on the Go side (identity/identity_id.go).
 * Splits on the LAST "@" and the LAST "/" — the key alphabet never contains
 * either character, so this is unambiguous even if userID/serverID/fingerprint
 * themselves happened to contain one.
 */
export type CanonicalKeyId = {
  userId: string;
  serverId: string;
  fingerprint: string;
};

export function parseKeyId(raw: string | null | undefined): CanonicalKeyId | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const slash = trimmed.lastIndexOf('/');
  if (slash <= 0 || slash === trimmed.length - 1) return null;
  const at = trimmed.lastIndexOf('@', slash - 1);
  if (at <= 0 || at === slash - 1) return null;
  const userId = trimmed.slice(0, at).trim();
  const serverId = trimmed.slice(at + 1, slash).trim();
  const fingerprint = trimmed.slice(slash + 1).trim();
  if (!userId || !serverId || !fingerprint) return null;
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
