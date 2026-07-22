/**
 * Local-only key-nest assemble-ability check for recovery progress (proposal 11).
 * Full wire KeyNode construction for claim/peer POST bodies lands in 12/13.
 */

import type * as api from '$lib/types/api';

export type NestAssembleResult = { ok: true } | { ok: false; reason: string };

export type KeyNestLookups = {
  getUser: (userId: string) => api.User | null | undefined;
  getPublicKey: (fingerprint: string) => api.PublicKey | null | undefined;
};

/**
 * Walk activeKeyFingerprint → predecessor using only local IndexedDB-shaped
 * lookups. No network. Buildable iff every hop has armor + server and the
 * chain ends at a signup key (predecessor == null), without gaps or cycles.
 */
export function canAssembleKeyNest(
  userId: string,
  lookups: KeyNestLookups
): NestAssembleResult {
  const user = lookups.getUser(userId);
  if (!user) {
    return { ok: false, reason: 'missing profile' };
  }
  const startFp = user.activeKeyFingerprint;
  if (!startFp) {
    return { ok: false, reason: 'missing activeKeyFingerprint' };
  }

  const visited = new Set<string>();
  let fingerprint = startFp;

  for (;;) {
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
    if (!key.server) {
      return { ok: false, reason: `missing server countersignature for ${fingerprint}` };
    }

    if (key.predecessor == null) {
      return { ok: true };
    }
    if (!key.predecessor.fingerprint) {
      return {
        ok: false,
        reason: `incomplete predecessor on ${fingerprint}`,
      };
    }
    fingerprint = key.predecessor.fingerprint;
  }
}
