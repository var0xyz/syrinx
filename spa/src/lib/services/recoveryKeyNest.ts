/**
 * Local key-nest assemble-ability check and wire KeyNode construction for
 * claim / peer identity POST bodies.
 */

import type * as api from '$lib/types/api';

export type NestAssembleResult = { ok: true } | { ok: false; reason: string };

export type KeyNestLookups = {
  getUser: (userId: string) => api.User | null | undefined;
  /** Active signing key; from usersInfo (or legacy field on old backups). */
  getActiveKeyFingerprint: (userId: string) => string | null | undefined;
  getPublicKey: (fingerprint: string) => api.PublicKey | null | undefined;
};

export type KeyNestBuildLookups = KeyNestLookups & {
  getRevocation: (
    fingerprint: string
  ) => api.KeyRevocation | null | undefined;
};

export type NestBuildResult =
  | { ok: true; key: api.RecoveryKeyNode }
  | { ok: false; reason: string };

/**
 * Walk activeKeyFingerprint → predecessor using only local IndexedDB-shaped
 * lookups. No network. Buildable iff every hop has armor + server and the
 * chain ends at a signup key (predecessor == null), without gaps or cycles.
 */
export function canAssembleKeyNest(
  userId: string,
  lookups: KeyNestLookups
): NestAssembleResult {
  const built = buildKeyNest(userId, {
    ...lookups,
    getRevocation: () => null,
  });
  if (built.ok === false) {
    return { ok: false, reason: built.reason };
  }
  return { ok: true };
}

/**
 * Build the recursive wire nest for claim / peer identity from local stores.
 * Puts PublicKey.predecessor.signature on the child node's `signature`
 * (older key's sig over parent armor). Attaches revocation by fingerprint
 * or null.
 */
export function buildKeyNest(
  userId: string,
  lookups: KeyNestBuildLookups
): NestBuildResult {
  const user = lookups.getUser(userId);
  if (!user) {
    return { ok: false, reason: 'missing profile' };
  }
  const startFp =
    lookups.getActiveKeyFingerprint(userId) ||
    (user as api.User & { activeKeyFingerprint?: string }).activeKeyFingerprint;
  if (!startFp) {
    return { ok: false, reason: 'missing activeKeyFingerprint' };
  }

  const visited = new Set<string>();

  function buildNode(
    fingerprint: string,
    linkSignature?: string
  ): NestBuildResult {
    const fpKey = fingerprint.toLowerCase();
    if (visited.has(fpKey)) {
      return { ok: false, reason: `cycle at ${fingerprint}` };
    }
    visited.add(fpKey);

    const key = lookups.getPublicKey(fingerprint);
    if (!key) {
      return { ok: false, reason: `missing public key ${fingerprint}` };
    }
    if (!key.armor) {
      return { ok: false, reason: `missing armor for ${fingerprint}` };
    }
    if (!key.serverSignature) {
      return {
        ok: false,
        reason: `missing server countersignature for ${fingerprint}`,
      };
    }

    let predecessor: api.RecoveryKeyNode | null = null;
    if (key.predecessor != null) {
      if (!key.predecessor.fingerprint) {
        return {
          ok: false,
          reason: `incomplete predecessor on ${fingerprint}`,
        };
      }
      if (!key.predecessor.signature) {
        return {
          ok: false,
          reason: `missing predecessor signature on ${fingerprint}`,
        };
      }
      const child = buildNode(
        key.predecessor.fingerprint,
        key.predecessor.signature
      );
      if (child.ok === false) {
        return child;
      }
      predecessor = child.key;
    }

    const revocation = lookups.getRevocation(fingerprint) ?? null;
    const node: api.RecoveryKeyNode = {
      fingerprint: key.fingerprint,
      userID: key.userID,
      armor: key.armor,
      revoked: key.revoked,
      serverSignature: key.serverSignature,
      revocation,
      predecessor,
    };
    if (key.createdAt !== undefined) {
      node.createdAt = key.createdAt;
    }
    if (key.expiresAt !== undefined) {
      node.expiresAt = key.expiresAt;
    }
    if (linkSignature) {
      node.signature = linkSignature;
    }
    return { ok: true, key: node };
  }

  return buildNode(startFp);
}
