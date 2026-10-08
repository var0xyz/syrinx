import { writable } from 'svelte/store';
import type * as api from '$lib/types/api';
import { blockedByRepository } from '$lib/repositories/blockedBy';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { userInfoRepository } from '$lib/repositories/userInfo';
import { userListsRepository } from '$lib/repositories/userLists';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { pendingEvictionsRepository } from '$lib/repositories/pendingEvictions';
import { pendingKeyEvictionsRepository } from '$lib/repositories/pendingKeyEvictions';
import { reedRequestsRepository } from '$lib/repositories/reedRequests';
import { dbService } from './db';
import { serverConnection } from './serverConnection';
import { syncPendingEvictions } from './eviction';

/** Bumped whenever a block of the viewer is stored or dropped, so an open
 * profile page can re-check it. */
export const blockedByChanged = writable(0);

/** Stores a verified block of the viewer and drops what it held of the
 * blocking user, and the follow with its list memberships. Their keys
 * stay, to verify the block. False when the cert doesn't verify. */
export async function commitBlockLocally(cert: api.BlockCert): Promise<boolean> {
  try {
    await blockedByRepository.put(cert);
  } catch (error) {
    console.warn('[blockedBy] refused block', cert?.userID, error);
    return false;
  }
  const userID = cert.userID;

  await reedsService.deleteReedsByAuthor(userID);
  for (const record of await pendingEvictionsRepository.getByUser(userID)) {
    await pendingEvictionsRepository.delete(record.reedID);
  }
  for (const record of await reedRequestsRepository.getAllPending()) {
    if (record.reedId.startsWith(`${userID}/`)) await reedRequestsRepository.delete(record.requestId);
  }
  await userRepository.delete(userID);
  await userInfoRepository.delete(userID);

  await dbService.delete('following', userID);
  await dbService.delete('pendingFollows', userID);
  await dbService.delete('unfollow', userID);
  await userListsRepository.removeMember(userID);

  blockedByChanged.update((n) => n + 1);
  return true;
}

/** Handles a pushed or refusal USER_BLOCKED: commits it and acks it. */
export async function receiveBlock(cert: api.BlockCert): Promise<void> {
  if (await commitBlockLocally(cert)) {
    serverConnection.sendUserBlockedAck(cert.userID);
  }
}

/** A lift: the block goes, nothing it dropped comes back. */
export async function receiveUnblock(userID: string): Promise<void> {
  await blockedByRepository.delete(userID);
  blockedByChanged.update((n) => n + 1);
  serverConnection.sendUserUnblockedAck(userID);
}

/** Drops a block of the viewer and the blocking user's keys with it, from
 * Stored users. Their profile fetches it again the next time it is opened. */
export async function forgetBlock(userID: string): Promise<void> {
  for (const key of await publicKeyRepository.listPublicKeys()) {
    if (key.userID === userID) await pendingKeyEvictionsRepository.put({ keyID: key.id, userID });
  }
  await blockedByRepository.delete(userID);
  blockedByChanged.update((n) => n + 1);
  void syncPendingEvictions();
}
