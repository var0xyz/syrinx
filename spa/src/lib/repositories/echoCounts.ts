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
    const row = await dbService.get<EchoCountRow>('echo_counts', reedID);
    return row?.count ?? 0;
  },

  async put(reedID: string, count: number): Promise<void> {
    await dbService.put('echo_counts', { reedID, count }, allowUnsigned);
  },
};
