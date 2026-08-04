/**
 * Peer identity report-back during SPA recovery.
 * One peer per call; reuses the key-nest builder from own-identity claim.
 */

import type * as api from '$lib/types/api';
import { apiService } from './api';
import { dbService } from './db';
import { buildKeyNest } from './recoveryKeyNest';

export type ReportPeerResult =
  | { status: 'reported'; user: api.User }
  | { status: 'skipped'; reason: string };

/**
 * Report one peer's profile + full key nest to a recovery-mode server.
 * Skips (does not POST) when a full nest cannot be assembled locally.
 * Rejects the caller's own user ID — that must go through claim.
 */
export async function reportPeerIdentity(
  peerUserId: string
): Promise<ReportPeerResult> {
  const selfUserId = localStorage.getItem('userId');
  if (!selfUserId) {
    throw new Error('Missing local userId; restore a backup first.');
  }
  if (peerUserId === selfUserId) {
    throw new Error('Own user ID must use claim, not peer report.');
  }

  await dbService.init();
  const [users, usersInfo, publicKeys, revocations] = await Promise.all([
    dbService.getAll<api.User>('users'),
    dbService.getAll<api.UserInfo>('usersInfo'),
    dbService.getAll<api.PublicKey>('publicKeys'),
    dbService.getAll<api.KeyRevocation>('revocations'),
  ]);

  const usersById = new Map(users.map((u) => [u.id, u]));
  const infoByUserId = new Map(usersInfo.map((i) => [i.id, i]));
  const keysByFp = new Map(
    publicKeys.map((k) => [k.fingerprint.toLowerCase(), k])
  );
  const revocationsByFp = new Map(
    revocations.map((r) => [r.fingerprint.toLowerCase(), r])
  );

  const profile = usersById.get(peerUserId);
  if (!profile) {
    return { status: 'skipped', reason: 'missing profile' };
  }

  const nest = buildKeyNest(peerUserId, {
    getUser: (id) => usersById.get(id),
    getActiveKeyFingerprint: (id) =>
      infoByUserId.get(id)?.activeKeyFingerprint ||
      (usersById.get(id) as api.User & { activeKeyFingerprint?: string } | undefined)
        ?.activeKeyFingerprint,
    getPublicKey: (fp) => keysByFp.get(fp.toLowerCase()),
    getRevocation: (fp) => revocationsByFp.get(fp.toLowerCase()) ?? null,
  });
  if (nest.ok === false) {
    return { status: 'skipped', reason: nest.reason };
  }

  const user = await apiService.reportPeerIdentity({
    profile,
    key: nest.key,
  });
  return { status: 'reported', user };
}
