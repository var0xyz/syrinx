import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/** When the viewer last opened someone's profile. Local only. */
export interface ProfileVisit {
  userID: string;
  visitedAt: number;
}

export const profileVisitsRepository = {
  async record(userID: string): Promise<void> {
    await dbService.put<ProfileVisit>('profileVisits', { userID, visitedAt: Date.now() }, allowUnsigned);
  },

  async delete(userID: string): Promise<void> {
    await dbService.delete('profileVisits', userID);
  },

  async getAll(): Promise<ProfileVisit[]> {
    return dbService.getAll<ProfileVisit>('profileVisits');
  },
};
