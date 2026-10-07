import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';
import { verifyThreadRecord } from '$lib/verifiers';

export const threadsRepository = {
  async put(record: api.ThreadRecord): Promise<void> {
    await dbService.put('threads', record, verifyThreadRecord);
  },

  async get(threadID: string): Promise<api.ThreadRecord | null> {
    return dbService.get<api.ThreadRecord>('threads', threadID);
  },

  /** The locally held parts of threadID, in index order. */
  async getParts(threadID: string): Promise<ReedType[]> {
    return dbService.getAllByIndex<ReedType>(
      'reeds',
      'thread',
      IDBKeyRange.bound([threadID, 0], [threadID, Infinity])
    );
  },
};
