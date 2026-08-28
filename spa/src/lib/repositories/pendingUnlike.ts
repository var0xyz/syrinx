import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { likedReedsRepository } from '$lib/repositories/likedReeds';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingUnlikeRecord {
  compositeKey: string; // reedRef (authorID@serverID/uuid)
  reedID: string;
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingUnlikeSynced = writable(0);

export const pendingUnlikeRepository = {
  async put(record: PendingUnlikeRecord): Promise<void> {
    await dbService.put('pendingUnlike', record, allowUnsigned);
  },

  async delete(reedRef: string): Promise<void> {
    await dbService.delete('pendingUnlike', reedRef);
  },

  async get(reedRef: string): Promise<PendingUnlikeRecord | null> {
    return dbService.get<PendingUnlikeRecord>('pendingUnlike', reedRef);
  },

  async getAll(): Promise<PendingUnlikeRecord[]> {
    return dbService.getAll<PendingUnlikeRecord>('pendingUnlike');
  },

  /** Idempotent DELETE flush for offline-queued unlikes. No signing. */
  async syncPending(): Promise<void> {
    const pending = await pendingUnlikeRepository.getAll();
    for (const record of pending) {
      try {
        await apiService.unlikeReed(record.reedID);
        await likedReedsRepository.delete(record.reedID);
        await pendingUnlikeRepository.delete(record.reedID);
        pendingUnlikeSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending reed unlike:', record.reedID, error);
      }
    }
  },
};
