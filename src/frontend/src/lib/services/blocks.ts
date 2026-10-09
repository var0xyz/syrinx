import { writable } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildBlockUserPayload } from './signing';
import {
  blocksRepository,
  pendingBlocksRepository,
  pendingUnblocksRepository,
  type PendingBlockRecord,
} from '$lib/repositories/blocks';

/** Bumped whenever the viewer's own blocks change. */
export const blocksChanged = writable(0);

function changed(): void {
  blocksChanged.update((n) => n + 1);
}

/** A refusal that retrying can't fix: the user is gone or never existed. */
function isPermanent(error: unknown): boolean {
  const status = (error as { status?: number })?.status;
  return status === 400 || status === 404 || status === 409 || status === 410;
}

/** Whether the viewer blocks userID, counting actions still queued. */
export async function isBlocking(userID: string): Promise<boolean> {
  if (await pendingBlocksRepository.get(userID)) return true;
  if (await pendingUnblocksRepository.get(userID)) return false;
  return !!(await blocksRepository.get(userID));
}

/** Signs a block of userID, queues it, and sends it. A queued lift of the
 * same user cancels out instead. */
export async function block(userID: string): Promise<void> {
  if (await pendingUnblocksRepository.get(userID)) {
    await pendingUnblocksRepository.delete(userID);
    changed();
    if (await blocksRepository.get(userID)) return;
  }
  const viewerID = localStorage.getItem('userId');
  const keyId = authService.getActiveKeyId();
  if (!viewerID || !keyId) throw new Error('Not signed in');

  const signature = await requestSigner.sign(buildBlockUserPayload(viewerID, userID, keyId));
  const record = { blockedUserID: userID, keyId, signature };
  await pendingBlocksRepository.put(record);
  changed();
  await flushBlock(record);
}

/** Queues a lift of the viewer's block of userID and sends it. A queued
 * block of the same user cancels out instead. */
export async function unblock(userID: string): Promise<void> {
  if (await pendingBlocksRepository.get(userID)) {
    await pendingBlocksRepository.delete(userID);
    changed();
    if (!(await blocksRepository.get(userID))) return;
  }
  await pendingUnblocksRepository.put({ blockedUserID: userID });
  changed();
  await flushUnblock(userID);
}

async function flushBlock(record: PendingBlockRecord): Promise<void> {
  try {
    const cert = await apiService.blockUser(record.blockedUserID, record.signature, record.keyId);
    await blocksRepository.put(cert);
    await pendingBlocksRepository.delete(record.blockedUserID);
  } catch (error) {
    console.error('[blocks] could not send block', record.blockedUserID, error);
    if (isPermanent(error)) await pendingBlocksRepository.delete(record.blockedUserID);
  }
  changed();
}

async function flushUnblock(userID: string): Promise<void> {
  try {
    await apiService.unblockUser(userID);
    await blocksRepository.delete(userID);
    await pendingUnblocksRepository.delete(userID);
  } catch (error) {
    console.error('[blocks] could not send unblock', userID, error);
    if (isPermanent(error)) await pendingUnblocksRepository.delete(userID);
  }
  changed();
}

/** Sends whatever blocks and lifts are still queued. */
export async function syncPendingBlocks(): Promise<void> {
  for (const record of await pendingBlocksRepository.getAll()) await flushBlock(record);
  for (const record of await pendingUnblocksRepository.getAll()) await flushUnblock(record.blockedUserID);
}

/** Matches the local blocks to the server's, leaving queued actions alone. */
export async function reconcileBlocks(): Promise<void> {
  const remote = await apiService.listBlocks();
  const remoteIDs = new Set(remote.map((cert) => cert.blockedUserId));
  for (const cert of remote) {
    if (await blocksRepository.get(cert.blockedUserId)) continue;
    try {
      await blocksRepository.put(cert);
    } catch (error) {
      console.warn('[blocks] refused listed block', cert.blockedUserId, error);
    }
  }
  for (const cert of await blocksRepository.getAll()) {
    if (!remoteIDs.has(cert.blockedUserId) && !(await pendingBlocksRepository.get(cert.blockedUserId))) {
      await blocksRepository.delete(cert.blockedUserId);
    }
  }
  changed();
}
