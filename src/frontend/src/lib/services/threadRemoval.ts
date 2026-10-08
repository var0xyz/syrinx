import type * as api from '$lib/types/api';
import { dbService } from './db';
import { reedRemovalCommitted } from './reedRemoval';
import { threadsRepository } from '$lib/repositories/threads';

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
