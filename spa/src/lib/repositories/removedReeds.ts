import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyReedRemoval } from '$lib/verifiers';

export const removedReedsRepository = {
  async put(cert: api.ReedRemoval): Promise<void> {
    await dbService.put('removedReeds', cert, verifyReedRemoval);
  },

  async get(reedID: string): Promise<api.ReedRemoval | null> {
    return dbService.get<api.ReedRemoval>('removedReeds', reedID);
  },

  async has(reedID: string): Promise<boolean> {
    return !!(await removedReedsRepository.get(reedID));
  },
};
