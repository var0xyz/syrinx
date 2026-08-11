import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { likedReedsRepository } from '$lib/repositories/likedReeds';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingUnlikeRecord {
  compositeKey: string; // `${authorID}:${reedID}`
  authorID: string;
  reedID: string;
}

export function unlikeCompositeKey(authorID: string, reedID: string): string {
  return `${authorID}:${reedID}`;
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingUnlikeSynced = writable(0);

export const pendingUnlikeRepository = {
  async put(record: PendingUnlikeRecord): Promise<void> {
    await dbService.put('pendingUnlike', record, allowUnsigned);
  },

  async delete(authorID: string, reedID: string): Promise<void> {
    await dbService.delete('pendingUnlike', unlikeCompositeKey(authorID, reedID));
  },

  async get(authorID: string, reedID: string): Promise<PendingUnlikeRecord | null> {
    return dbService.get<PendingUnlikeRecord>('pendingUnlike', unlikeCompositeKey(authorID, reedID));
  },

  async getAll(): Promise<PendingUnlikeRecord[]> {
    return dbService.getAll<PendingUnlikeRecord>('pendingUnlike');
  },

  /** Idempotent DELETE flush for offline-queued unlikes. No signing. */
  async syncPending(): Promise<void> {
    const pending = await pendingUnlikeRepository.getAll();
    for (const record of pending) {
      try {
        await apiService.unlikeReed(record.authorID, record.reedID);
        await likedReedsRepository.delete(record.authorID, record.reedID);
        await pendingUnlikeRepository.delete(record.authorID, record.reedID);
        pendingUnlikeSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending reed unlike:', record.reedID, error);
      }
    }
  },
};
