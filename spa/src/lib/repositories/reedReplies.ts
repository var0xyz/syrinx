/**
 * Local cache of reply index rows — one entry per reply reed.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import type { ReedType } from '$lib/types/reed';
import type * as api from '$lib/types/api';
import { reedThreadsRepository } from '$lib/repositories/reedThreads';
import { parseReedRef } from '$lib/utils/reedRef';

export type ReedReplyRow = {
  reedID: string;
  userID: string;
  parent: { userID: string; reedID: string };
  parentUserID: string;
  parentReedID: string;
  parentKey: string;
  threadId: string;
};

export function parentKey(parentUserID: string, parentReedID: string): string {
  return `${parentUserID}/${parentReedID}`;
}

function rowFromFields(
  replyUserID: string,
  replyReedID: string,
  parentUserID: string,
  parentReedID: string,
  threadId: string,
): ReedReplyRow {
  return {
    reedID: replyReedID,
    userID: replyUserID,
    parent: { userID: parentUserID, reedID: parentReedID },
    parentUserID,
    parentReedID,
    parentKey: parentKey(parentUserID, parentReedID),
    threadId,
  };
}

export const reedRepliesRepository = {
  async put(row: ReedReplyRow): Promise<void> {
    await dbService.put('reed_replies', row, allowUnsigned);
    await reedThreadsRepository.ensureFromThreadId(row.threadId);
  },

  async upsertFromMeta(
    reply: api.ReplyMeta,
    parentUserID: string,
    parentReedID: string,
    threadId: string,
  ): Promise<void> {
    await reedRepliesRepository.put(
      rowFromFields(reply.userID, reply.reedID, parentUserID, parentReedID, threadId),
    );
  },

  async upsertFromReed(reed: Pick<ReedType, 'id' | 'userID' | 'threadId' | 'replying'>): Promise<void> {
    if (!reed.replying || !reed.threadId) return;
    const parent = parseReedRef(reed.replying);
    if (!parent) return;
    await reedRepliesRepository.put(
      rowFromFields(reed.userID, reed.id, parent.authorId, parent.reedId, reed.threadId),
    );
  },

  async syncFromServerList(
    parentUserID: string,
    parentReedID: string,
    threadId: string,
    replies: api.ReplyMeta[],
  ): Promise<void> {
    for (const reply of replies) {
      await reedRepliesRepository.upsertFromMeta(reply, parentUserID, parentReedID, threadId);
    }
  },

  async listByParent(parentUserID: string, parentReedID: string): Promise<ReedReplyRow[]> {
    const key = parentKey(parentUserID, parentReedID);
    return dbService.getAllByIndex<ReedReplyRow>('reed_replies', 'parentKey', key);
  },

  async remove(reedID: string): Promise<void> {
    await dbService.delete('reed_replies', reedID);
  },
};
