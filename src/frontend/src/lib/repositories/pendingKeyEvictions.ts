import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/** One public key awaiting its KEY_EVICTION_ACK. userID is its owner, so
 * the storage view can show them as still evicting. */
export interface PendingKeyEvictionRecord {
  keyID: string;
  userID: string;
}

export const pendingKeyEvictionsRepository = {
  async put(record: PendingKeyEvictionRecord): Promise<void> {
    await dbService.put('pendingKeyEvictions', record, allowUnsigned);
  },

  async delete(keyID: string): Promise<void> {
    await dbService.delete('pendingKeyEvictions', keyID);
  },

  async getAll(): Promise<PendingKeyEvictionRecord[]> {
    return dbService.getAll<PendingKeyEvictionRecord>('pendingKeyEvictions');
  },

  /** Queued keys for one user — empty means their last key is gone. */
  async getByUser(userID: string): Promise<PendingKeyEvictionRecord[]> {
    const all = await dbService.getAll<PendingKeyEvictionRecord>('pendingKeyEvictions');
    return all.filter((record) => record.userID === userID);
  },
};
