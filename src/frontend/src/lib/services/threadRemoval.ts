import type * as api from '$lib/types/api';
import { dbService } from './db';
import { reedRemovalCommitted } from './reedRemoval';
import { get } from 'svelte/store';
import { threadsRepository } from '$lib/repositories/threads';
import { pendingRemovalRepository } from '$lib/repositories/pendingRemoval';
import { apiService } from './api';
import { requestSigner } from './request-signer';
import { serverInfo } from './serverInfo';
import { buildThreadRemovalUserPayload } from './signing';

/**
 * Applies a thread removal: stores the verified certificate first, then
 * drops the record, every listed part, and any local part naming the thread.
 * Returns false, changing nothing, when it doesn't verify.
 */
export async function applyThreadRemoval(removal: api.ThreadRemoval): Promise<boolean> {
  try {
    await threadsRepository.putRemoval(removal);
  } catch (error) {
    console.error('[applyThreadRemoval] refused', removal?.threadID, error);
    return false;
  }
  const local = await threadsRepository.getParts(removal.threadID);
  const ids = new Set([...removal.record.reedIDs, ...local.map((reed) => reed.id)]);
  await Promise.all([...ids].map((id) => dbService.delete('reeds', id)));
  await threadsRepository.delete(removal.threadID);
  reedRemovalCommitted.update((n) => n + 1);
  return true;
}

/**
 * Author path: sign a removal bound to the thread record's signature, queue
 * it, DELETE, then apply the countersigned removal and clear the queue.
 */
export async function removeThreadAsAuthor(threadID: string): Promise<void> {
  const record = await threadsRepository.get(threadID);
  if (!record) throw new Error('Thread record not held');
  const serverID = get(serverInfo)?.id || localStorage.getItem('serverId');
  if (!serverID) throw new Error('Server ID not available');

  const signature = await requestSigner.sign(
    buildThreadRemovalUserPayload(serverID, threadID, record.userSignature.armor)
  );
  await pendingRemovalRepository.put({ reedID: threadID, kind: 'thread', serverID, signature });
  const removal = await apiService.deleteThread(threadID, signature);
  if (!(await applyThreadRemoval(removal))) {
    throw new Error('Server thread-removal countersignature failed verification');
  }
  await pendingRemovalRepository.delete(threadID);
}
