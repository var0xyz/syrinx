import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyAccountRemoval } from '$lib/verifiers';

export const removedAccountsRepository = {
  async put(cert: api.AccountRemoval): Promise<void> {
    await dbService.put('removedAccounts', cert, verifyAccountRemoval);
  },

  async get(userID: string): Promise<api.AccountRemoval | null> {
    return dbService.get<api.AccountRemoval>('removedAccounts', userID);
  },

  async has(userID: string): Promise<boolean> {
    return !!(await removedAccountsRepository.get(userID));
  },
};
