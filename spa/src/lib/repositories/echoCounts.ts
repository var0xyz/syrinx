/**
 * Local cache of server echo counts keyed by reed id.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

export type EchoCountRow = {
  reedID: string;
  count: number;
};

export const echoCountsRepository = {
  async get(reedID: string): Promise<number> {
    const row = await dbService.get<EchoCountRow>('echoCounts', reedID);
    return row?.count ?? 0;
  },

  async put(reedID: string, count: number): Promise<void> {
    await dbService.put('echoCounts', { reedID, count }, allowUnsigned);
  },
};
