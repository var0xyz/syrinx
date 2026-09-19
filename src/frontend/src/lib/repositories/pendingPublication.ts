import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingPublicationRecord {
  reedID: string;
}

export const pendingPublicationRepository = {
  async put(reedID: string): Promise<void> {
    await dbService.put('pendingPublication', { reedID }, allowUnsigned);
  },

  async delete(reedID: string): Promise<void> {
    await dbService.delete('pendingPublication', reedID);
  },

  async getAll(): Promise<PendingPublicationRecord[]> {
    return dbService.getAll<PendingPublicationRecord>('pendingPublication');
  },
};
