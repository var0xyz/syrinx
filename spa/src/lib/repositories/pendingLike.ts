import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { verifyAndCommitReedLike } from '$lib/services/reedLike';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingLikeRecord {
  compositeKey: string; // reedRef (authorID@serverID/uuid)
  reedID: string;
  serverID: string;
  keyId: string;
  signature: string; // base64 user detached sig
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingLikeSynced = writable(0);

export const pendingLikeRepository = {
  async put(record: PendingLikeRecord): Promise<void> {
    await dbService.put('pendingLikes', record, allowUnsigned);
  },

  async delete(reedRef: string): Promise<void> {
    await dbService.delete('pendingLikes', reedRef);
  },

  async get(reedRef: string): Promise<PendingLikeRecord | null> {
    return dbService.get<PendingLikeRecord>('pendingLikes', reedRef);
  },

  async getAll(): Promise<PendingLikeRecord[]> {
    return dbService.getAll<PendingLikeRecord>('pendingLikes');
  },

  /** Idempotent POST flush for offline-queued likes. */
  async syncPending(): Promise<void> {
    const pending = await pendingLikeRepository.getAll();
    for (const record of pending) {
      try {
        const cert = await apiService.likeReed(record.reedID, record.signature, record.keyId);
        if (!(await verifyAndCommitReedLike(cert))) {
          console.error('Failed to verify reed like cert on flush:', record.reedID);
          continue;
        }
        await pendingLikeRepository.delete(record.reedID);
        pendingLikeSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending reed like:', record.reedID, error);
      }
    }
  },
};
