/**
 * Local cache of thread roots keyed by threadId wire ref.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { parseReedRef } from '$lib/utils/reedRef';

export type ReedThreadRow = {
  id: string;
  userID: string;
  reedID: string;
};

export const reedThreadsRepository = {
  async get(threadId: string): Promise<ReedThreadRow | null> {
    return dbService.get<ReedThreadRow>('reed_threads', threadId);
  },

  async put(row: ReedThreadRow): Promise<void> {
    await dbService.put('reed_threads', row, allowUnsigned);
  },

  /** Upsert root from a threadId header (userID@serverID/reedID). */
  async ensureFromThreadId(threadId: string): Promise<void> {
    const parsed = parseReedRef(threadId);
    if (!parsed) return;
    await reedThreadsRepository.put({
      id: threadId,
      userID: parsed.authorId,
      reedID: parsed.reedId,
    });
  },
};
