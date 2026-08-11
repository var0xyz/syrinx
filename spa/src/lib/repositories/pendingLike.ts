import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { verifyAndCommitReedLike } from '$lib/services/reedLike';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingLikeRecord {
  compositeKey: string; // `${authorID}:${reedID}`
  authorID: string;
  reedID: string;
  serverID: string;
  fingerprint: string;
  signature: string; // base64 user detached sig
}

export function likeCompositeKey(authorID: string, reedID: string): string {
  return `${authorID}:${reedID}`;
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingLikeSynced = writable(0);

export const pendingLikeRepository = {
  async put(record: PendingLikeRecord): Promise<void> {
    await dbService.put('pendingLikes', record, allowUnsigned);
  },

  async delete(authorID: string, reedID: string): Promise<void> {
    await dbService.delete('pendingLikes', likeCompositeKey(authorID, reedID));
  },

  async get(authorID: string, reedID: string): Promise<PendingLikeRecord | null> {
    return dbService.get<PendingLikeRecord>('pendingLikes', likeCompositeKey(authorID, reedID));
  },

  async getAll(): Promise<PendingLikeRecord[]> {
    return dbService.getAll<PendingLikeRecord>('pendingLikes');
  },

  /** Idempotent POST flush for offline-queued likes. */
  async syncPending(): Promise<void> {
    const pending = await pendingLikeRepository.getAll();
    for (const record of pending) {
      try {
        const cert = await apiService.likeReed(
          record.authorID,
          record.reedID,
          record.signature,
          record.fingerprint
        );
        if (!(await verifyAndCommitReedLike(cert))) {
          console.error('Failed to verify reed like cert on flush:', record.reedID);
          continue;
        }
        await pendingLikeRepository.delete(record.authorID, record.reedID);
        pendingLikeSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending reed like:', record.reedID, error);
      }
    }
  },
};
