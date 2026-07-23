import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';

export const removedAccountsRepository = {
  async put(cert: api.AccountRemoval): Promise<void> {
    await dbService.put('removedAccounts', cert);
  },

  async get(userID: string): Promise<api.AccountRemoval | null> {
    return dbService.get<api.AccountRemoval>('removedAccounts', userID);
  },
};
