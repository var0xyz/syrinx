import { dbService } from './db';
import { serverConnection } from './serverConnection';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { userRepository } from '$lib/repositories/user';
import { listsRepository } from '$lib/repositories/lists';
import {
  pendingEvictionsRepository,
  type PendingEvictionRecord,
} from '$lib/repositories/pendingEvictions';
import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';

/** A user whose locally-held content this device may drop. reedIDs is
 * empty for a cached profile we hold no reeds for — still worth evicting,
 * since the profile and key are themselves more than the reed would be. */
type EvictionCandidate = {
  userID: string;
  reedIDs: string[];
};

/** Users the viewer has shown interest in, plus the viewer: never
 * evicted even when over quota. */
async function protectedUserIDs(): Promise<Set<string>> {
  const protectedIDs = new Set<string>();

  const viewerID = localStorage.getItem('userId');
  if (viewerID) protectedIDs.add(viewerID);

  for (const { userId } of await dbService.getAll<{ userId: string }>('following')) {
    protectedIDs.add(userId);
  }
  for (const list of await listsRepository.getAll()) {
    for (const memberID of list.memberIds) protectedIDs.add(memberID);
  }

  return protectedIDs;
}

/** Every evictable local user, each with whatever reeds we hold for them.
 * Authors of held reeds and reedless cached profiles are both candidates;
 * a profile with no reeds is only ever reachable through the latter. */
async function listCandidates(): Promise<EvictionCandidate[]> {
  const protectedIDs = await protectedUserIDs();

  // Already queued: their reeds are on their way out, so offering them
  // again would evict a second user for space that's already coming.
  for (const record of await pendingEvictionsRepository.getAll()) {
    protectedIDs.add(record.userID);
  }

  const byAuthor = new Map<string, string[]>();
  for (const reed of await dbService.getAll<ReedType>('reeds')) {
    if (!reed.userID || protectedIDs.has(reed.userID)) continue;
    const held = byAuthor.get(reed.userID);
    if (held) {
      held.push(reed.id);
    } else {
      byAuthor.set(reed.userID, [reed.id]);
    }
  }

  // Tombstones are evictable like any other profile: the signed removal
  // cert lives in removedAccounts, and the profile page re-fetches it.
  for (const profile of await dbService.getAll<api.User>('users')) {
    if (!profile.id || protectedIDs.has(profile.id)) continue;
    if (!byAuthor.has(profile.id)) byAuthor.set(profile.id, []);
  }

  return [...byAuthor].map(([userID, reedIDs]) => ({ userID, reedIDs }));
}

function pickRandom<T>(items: T[]): T | null {
  if (items.length === 0) return null;
  return items[Math.floor(Math.random() * items.length)];
}

/** Drop the victim's profile, info and key. Called only once none of
 * their reeds are queued — the profile and key are what a retry needs to
 * verify whatever content is still held. */
async function dropUserRecords(userID: string): Promise<void> {
  const profile = await userRepository.get(userID);
  await userRepository.delete(userID);
  await dbService.delete('usersInfo', userID);

  const keyID = profile?.userSignature?.id;
  if (keyID) {
    await publicKeyRepository.deletePublicKey(keyID);
  }
}

/** Announce one queued reed and, once the server acks, delete it. The
 * victim's profile and key follow when this was their last queued reed. */
async function flushOne(record: PendingEvictionRecord): Promise<boolean> {
  if (!(await serverConnection.evict(record.reedID))) return false;

  await dbService.delete('reeds', record.reedID);
  await pendingEvictionsRepository.delete(record.reedID);

  if ((await pendingEvictionsRepository.getByUser(record.userID)).length === 0) {
    await dropUserRecords(record.userID);
  }
  return true;
}

/** Retry every queued eviction. Safe to call repeatedly: an already-acked
 * reed is gone from the queue, and the server acks unknown evictions. */
export async function syncPendingEvictions(): Promise<void> {
  for (const record of await pendingEvictionsRepository.getAll()) {
    try {
      await flushOne(record);
    } catch (error) {
      console.error('Eviction: failed to flush queued eviction:', record.reedID, error);
    }
  }
}

/**
 * Queue one random user for eviction and start draining in the
 * background. Returns as soon as the queue is written, so a store never
 * waits on the server: 85% of quota still leaves room for the new reed.
 */
export async function freeSpace(): Promise<void> {
  const candidate = pickRandom(await listCandidates());
  if (!candidate) {
    console.warn('Eviction: over quota threshold with no evictable users left');
    return;
  }

  if (candidate.reedIDs.length === 0) {
    await dropUserRecords(candidate.userID);
    return;
  }

  for (const reedID of candidate.reedIDs) {
    await pendingEvictionsRepository.put({ reedID, userID: candidate.userID });
  }

  void syncPendingEvictions();
}
