/**
 * Local cache of server direct-reply counts keyed by reed id.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

export type ReplyCountRow = {
  reedID: string;
  count: number;
};

export const replyCountsRepository = {
  async get(reedID: string): Promise<number> {
    const row = await dbService.get<ReplyCountRow>('reply_counts', reedID);
    return row?.count ?? 0;
  },

  async put(reedID: string, count: number): Promise<void> {
    await dbService.put('reply_counts', { reedID, count }, allowUnsigned);
  },
};
