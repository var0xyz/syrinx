/**
 * Local cache of reply index rows — one entry per reply reed.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import type { ReedType } from '$lib/types/reed';
import type * as api from '$lib/types/api';
import { parseReedRef } from '$lib/utils/reedRef';

export type ReedReplyRow = {
  reedID: string;
  userID: string;
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
    parentUserID,
    parentReedID,
    parentKey: parentKey(parentUserID, parentReedID),
    threadId,
  };
}

export const reedRepliesRepository = {
  async put(row: ReedReplyRow): Promise<void> {
    await dbService.put('reedReplies', row, allowUnsigned);
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

  // Prunes locally cached rows for replies the server no longer lists
  // (e.g. removed on a federated server before this client ever saw a
  // live removal notice) — without this, a stale row flashes on load
  // before the server refresh corrects the count.
  async pruneStale(
    parentUserID: string,
    parentReedID: string,
    liveReedIDs: Set<string>,
  ): Promise<void> {
    const cached = await reedRepliesRepository.listByParent(parentUserID, parentReedID);
    for (const row of cached) {
      if (!liveReedIDs.has(row.reedID)) {
        await reedRepliesRepository.remove(row.reedID);
      }
    }
  },

  async listByParent(parentUserID: string, parentReedID: string): Promise<ReedReplyRow[]> {
    return dbService.getAllByIndex<ReedReplyRow>('reedReplies', 'parentKey', parentKey(parentUserID, parentReedID));
  },

  async remove(reedID: string): Promise<void> {
    await dbService.delete('reedReplies', reedID);
  },
};
