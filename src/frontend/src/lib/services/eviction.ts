import { dbService } from './db';
import { serverConnection } from './serverConnection';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { userRepository } from '$lib/repositories/user';
import { userListsRepository } from '$lib/repositories/userLists';
import {
  pendingEvictionsRepository,
  type PendingEvictionRecord,
} from '$lib/repositories/pendingEvictions';
import {
  pendingKeyEvictionsRepository,
  type PendingKeyEvictionRecord,
} from '$lib/repositories/pendingKeyEvictions';
import { profileVisitsRepository } from '$lib/repositories/profileVisits';
import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';
import { buildProtectedUserIDs } from '$lib/utils/evictionProtection';

/** A user whose locally-held content this device may drop. reedIDs is
 * empty for a cached profile we hold no reeds for — still worth evicting,
 * since the profile and key are themselves more than the reed would be. */
type EvictionCandidate = {
  userID: string;
  reedIDs: string[];
};

/** Users the viewer has shown interest in, plus the viewer: never
 * evicted even when over quota, and their keys are never evicted at all. */
export async function protectedUserIDs(): Promise<Set<string>> {
  return buildProtectedUserIDs({
    viewerID: localStorage.getItem('userId'),
    following: await dbService.getAll<{ userId: string }>('following'),
    userLists: await userListsRepository.getAll(),
    vouches: await dbService.getAll<api.Vouch>('vouches'),
  });
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
  for (const record of await pendingKeyEvictionsRepository.getAll()) {
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

/** Queue the victim's keys; their profile goes once the last key is
 * acked. Called only once none of their reeds are queued — the profile
 * and key are what a retry needs to verify what is still held. */
async function dropUserRecords(userID: string): Promise<void> {
  // Content from a protected user keeps arriving and needs both.
  if ((await protectedUserIDs()).has(userID)) return;

  const keyIDs = new Set<string>();
  const profile = await userRepository.get(userID);
  if (profile?.userSignature?.id) keyIDs.add(profile.userSignature.id);
  for (const key of await publicKeyRepository.listPublicKeys()) {
    if (key.userID === userID) keyIDs.add(key.id);
  }

  if (keyIDs.size === 0) {
    await dropProfile(userID);
    return;
  }
  for (const keyID of keyIDs) {
    await pendingKeyEvictionsRepository.put({ keyID, userID });
  }
}

async function dropProfile(userID: string): Promise<void> {
  await userRepository.delete(userID);
  await dbService.delete('usersInfo', userID);
  await profileVisitsRepository.delete(userID);
}

/** Announce one queued key and, once the server acks, delete it. */
async function flushKey(record: PendingKeyEvictionRecord): Promise<boolean> {
  if (!(await serverConnection.evictKey(record.keyID))) return false;

  await publicKeyRepository.deletePublicKey(record.keyID);
  await pendingKeyEvictionsRepository.delete(record.keyID);

  if ((await pendingKeyEvictionsRepository.getByUser(record.userID)).length === 0) {
    await dropProfile(record.userID);
  }
  return true;
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
  for (const record of await pendingKeyEvictionsRepository.getAll()) {
    try {
      await flushKey(record);
    } catch (error) {
      console.error('Eviction: failed to flush queued key eviction:', record.keyID, error);
    }
  }
}

/** Queue everything held for each user; the caller drains the queue.
 * Their reeds go even if protected; a protected user's profile and keys stay. */
export async function evictUsers(userIDs: string[]): Promise<void> {
  const viewerID = localStorage.getItem('userId');
  for (const userID of userIDs) {
    if (userID === viewerID) continue;

    const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', userID);
    if (reeds.length === 0) {
      await dropUserRecords(userID);
      continue;
    }
    for (const reed of reeds) {
      await pendingEvictionsRepository.put({ reedID: reed.id, userID });
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
    void syncPendingEvictions();
    return;
  }

  for (const reedID of candidate.reedIDs) {
    await pendingEvictionsRepository.put({ reedID, userID: candidate.userID });
  }

  void syncPendingEvictions();
}
