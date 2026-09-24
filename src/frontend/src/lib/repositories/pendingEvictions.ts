import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/** One reed awaiting its EVICTION_ACK. userID is the victim it belongs
 * to, so the profile and key can be dropped once their last reed goes. */
export interface PendingEvictionRecord {
  reedID: string;
  userID: string;
}

export const pendingEvictionsRepository = {
  async put(record: PendingEvictionRecord): Promise<void> {
    await dbService.put('pendingEvictions', record, allowUnsigned);
  },

  async delete(reedID: string): Promise<void> {
    await dbService.delete('pendingEvictions', reedID);
  },

  async getAll(): Promise<PendingEvictionRecord[]> {
    return dbService.getAll<PendingEvictionRecord>('pendingEvictions');
  },

  /** Queued reeds for one victim — empty means their last reed is gone. */
  async getByUser(userID: string): Promise<PendingEvictionRecord[]> {
    const all = await dbService.getAll<PendingEvictionRecord>('pendingEvictions');
    return all.filter((record) => record.userID === userID);
  },
};
