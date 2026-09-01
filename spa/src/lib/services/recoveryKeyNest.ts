/**
 * Local key-nest assemble-ability check and wire KeyNode construction for
 * claim / peer identity POST bodies.
 *
 * Fingerprints on this wire (KeyNode.fingerprint, KeyRevocation.fingerprint)
 * travel BARE — recovery/wire.go's server-side "DELIBERATE EXCEPTION"
 * comment covers this too, not just userIDs: verifyKeyCountersig/
 * verifyRevocation pair a bare fingerprint with a bare userID inside the
 * exact bytes the server reconstructs to verify, so a canonical fingerprint
 * here would produce bytes that don't match what was actually signed.
 * Local lookups (publicKeys/revocations IndexedDB) are keyed canonically
 * though, so every fingerprint pulled from a lookup is extracted back to
 * bare before landing on the wire node.
 *
 * The predecessor handoff signature (KeyNode.signature) doesn't live on
 * PublicKey.predecessor (just an id) — it's one hop away, on the
 * predecessor's own KeyRevocation.successorSignature.
 */

import type * as api from '$lib/types/api';
import { parseKeyId } from '$lib/utils/identityRef';

function bareFingerprint(fingerprint: string): string {
  return parseKeyId(fingerprint)?.fingerprint ?? fingerprint;
}

export type NestAssembleResult = { ok: true } | { ok: false; reason: string };

export type KeyNestLookups = {
  getUser: (userId: string) => api.User | null | undefined;
  /** Active signing key; from usersInfo (or legacy field on old backups). */
  getActiveKeyId: (userId: string) => string | null | undefined;
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
 * Walk activeKeyID → predecessor using only local IndexedDB-shaped
 * lookups. No network. Buildable iff every hop has armor + server and the
 * chain ends at a signup key (predecessor == null), without gaps or cycles.
 *
 * Needs a real getRevocation (not a null stub): a rotation hop's handoff
 * proof lives on the predecessor's own revocation.successorSignature, not
 * on PublicKey.predecessor itself (see this file's header comment) — so
 * assembling any nest with a rotation requires revocation data.
 */
export function canAssembleKeyNest(
  userId: string,
  lookups: KeyNestBuildLookups
): NestAssembleResult {
  const built = buildKeyNest(userId, lookups);
  if (built.ok === false) {
    return { ok: false, reason: built.reason };
  }
  return { ok: true };
}

/**
 * Build the recursive wire nest for claim / peer identity from local stores.
 * Puts the predecessor's own revocation.successorSignature on the child
 * node's `signature` (older key's sig over parent armor) — that's where
 * the rotation handoff proof lives, one hop from PublicKey.predecessor.
 * Attaches each node's own revocation by fingerprint or null.
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
    lookups.getActiveKeyId(userId) ||
    (user as api.User & { activeKeyID?: string }).activeKeyID;
  if (!startFp) {
    return { ok: false, reason: 'missing activeKeyID' };
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

    const revocation = lookups.getRevocation(fingerprint) ?? null;
    // Built explicitly, not spread: RecoveryKeyRevocation is a narrower,
    // deliberately bare-fingerprint shape (recovery/wire.go) and has no
    // successorSignature field — that's /keys-API-only bookkeeping.
    const wireRevocation: api.RecoveryKeyRevocation | null = revocation
      ? {
          fingerprint: bareFingerprint(revocation.id),
          userID: revocation.userID,
          reason: revocation.reason,
          successor: revocation.successor,
          userSignature: revocation.userSignature,
          serverSignature: revocation.serverSignature,
        }
      : null;

    let predecessor: api.RecoveryKeyNode | null = null;
    if (key.predecessor != null) {
      // The handoff proof (predecessor's sig over this key's armor) lives
      // on the PREDECESSOR's own revocation row, not this key's.
      const predRevocation = lookups.getRevocation(key.predecessor) ?? null;
      if (!predRevocation?.successorSignature) {
        return {
          ok: false,
          reason: `missing predecessor handoff signature on ${fingerprint}`,
        };
      }
      const child = buildNode(key.predecessor, predRevocation.successorSignature);
      if (child.ok === false) {
        return child;
      }
      predecessor = child.key;
    }

    const node: api.RecoveryKeyNode = {
      fingerprint: bareFingerprint(key.id),
      userID: key.userID,
      armor: btoa(key.armor),
      revoked: key.revoked,
      serverSignature: key.serverSignature,
      revocation: wireRevocation,
      predecessor,
    };
    if (key.createdAt !== undefined) {
      node.createdAt = key.createdAt;
    }
    if (linkSignature) {
      node.signature = linkSignature;
    }
    return { ok: true, key: node };
  }

  return buildNode(startFp);
}
