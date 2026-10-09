import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';
import { verifyThreadRecord, verifyThreadRemoval } from '$lib/verifiers';

export const threadsRepository = {
  async put(record: api.ThreadRecord): Promise<void> {
    await dbService.put('threads', record, verifyThreadRecord);
  },

  async get(threadID: string): Promise<api.ThreadRecord | null> {
    return dbService.get<api.ThreadRecord>('threads', threadID);
  },

  async delete(threadID: string): Promise<void> {
    await dbService.delete('threads', threadID);
  },

  /** Stores a verified thread removal, permanently. */
  async putRemoval(removal: api.ThreadRemoval): Promise<void> {
    await dbService.put('removedThreads', removal, verifyThreadRemoval);
  },

  async isRemoved(threadID: string): Promise<boolean> {
    return !!(await dbService.get<api.ThreadRemoval>('removedThreads', threadID));
  },

  /** What a list needs to place one part: the thread's size when its record
   * is held, and the head and second part when they are. */
  async summary(threadID: string): Promise<{ total: number; head: ReedType | null; next: ReedType | null }> {
    const record = await threadsRepository.get(threadID);
    const parts = await threadsRepository.getParts(threadID);
    return {
      total: record?.reedIds.length ?? 0,
      head: parts.find((reed) => reed.thread?.index === 0) ?? null,
      next: parts.find((reed) => reed.thread?.index === 1) ?? null,
    };
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
