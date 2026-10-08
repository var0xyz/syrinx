import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyBlock } from '$lib/verifiers';

/** Verified blocks of the viewer, keyed by the blocking user. */
export const blockedByRepository = {
  async put(cert: api.BlockCert): Promise<void> {
    await dbService.put('blockedBy', cert, verifyBlock);
  },

  async get(userID: string): Promise<api.BlockCert | null> {
    return dbService.get<api.BlockCert>('blockedBy', userID);
  },

  async has(userID: string): Promise<boolean> {
    return !!(await blockedByRepository.get(userID));
  },

  async getAll(): Promise<api.BlockCert[]> {
    return dbService.getAll<api.BlockCert>('blockedBy');
  },

  async delete(userID: string): Promise<void> {
    await dbService.delete('blockedBy', userID);
  },
};
