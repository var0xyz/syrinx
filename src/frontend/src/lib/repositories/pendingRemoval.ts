import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
import { applyThreadRemoval } from '$lib/services/threadRemoval';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingRemovalRecord {
  /** A reed's ID, or a thread's head ID when kind is 'thread'. */
  reedID: string;
  kind: 'reed' | 'thread';
  serverID: string;
  signature: string; // armored user detached sig
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingRemovalSynced = writable(0);

export const pendingRemovalRepository = {
  async put(record: PendingRemovalRecord): Promise<void> {
    await dbService.put('pendingRemoval', record, allowUnsigned);
  },

  async delete(reedID: string): Promise<void> {
    await dbService.delete('pendingRemoval', reedID);
  },

  async get(reedID: string): Promise<PendingRemovalRecord | null> {
    return dbService.get<PendingRemovalRecord>('pendingRemoval', reedID);
  },

  async getAll(): Promise<PendingRemovalRecord[]> {
    return dbService.getAll<PendingRemovalRecord>('pendingRemoval');
  },

  /** Idempotent DELETE flush for offline-queued removals. */
  async syncPending(): Promise<void> {
    const pending = await dbService.getAll<PendingRemovalRecord>('pendingRemoval');
    for (const record of pending) {
      try {
        const committed =
          record.kind === 'thread'
            ? await applyThreadRemoval(await apiService.deleteThread(record.reedID, record.signature))
            : await verifyAndCommitReedRemoval(await apiService.deleteReed(record.reedID, record.signature));
        if (!committed) {
          console.error('Failed to verify removal cert on flush:', record.reedID);
          continue;
        }
        await pendingRemovalRepository.delete(record.reedID);
        pendingRemovalSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending reed removal:', record.reedID, error);
      }
    }
  },
};
