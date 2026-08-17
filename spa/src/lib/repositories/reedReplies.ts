/**
 * Local cache of reply index rows — one entry per reply reed.
 */

import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import type { ReedType } from '$lib/types/reed';
import type * as api from '$lib/types/api';

export type ReedReplyRow = {
  reedID: string;
  userID: string;
  /** Canonical ref (authorID/reedID) of the reed this one replies to. */
  parentReedID: string;
  threadId: string;
};

function rowFromFields(
  replyUserID: string,
  replyReedID: string,
  parentReedRef: string,
  threadId: string,
): ReedReplyRow {
  return {
    reedID: replyReedID,
    userID: replyUserID,
    parentReedID: parentReedRef,
    threadId,
  };
}

export const reedRepliesRepository = {
  async put(row: ReedReplyRow): Promise<void> {
    await dbService.put('reedReplies', row, allowUnsigned);
  },

  async upsertFromMeta(
    reply: api.ReplyMeta,
    parentReedRef: string,
    threadId: string,
  ): Promise<void> {
    await reedRepliesRepository.put(
      rowFromFields(reply.userID, reply.reedID, parentReedRef, threadId),
    );
  },

  async upsertFromReed(reed: Pick<ReedType, 'id' | 'userID' | 'threadId' | 'replying'>): Promise<void> {
    if (!reed.replying || !reed.threadId) return;
    await reedRepliesRepository.put(
      rowFromFields(reed.userID, reed.id, reed.replying, reed.threadId),
    );
  },

  async syncFromServerList(
    parentReedRef: string,
    threadId: string,
    replies: api.ReplyMeta[],
  ): Promise<void> {
    for (const reply of replies) {
      await reedRepliesRepository.upsertFromMeta(reply, parentReedRef, threadId);
    }
  },

  // Prunes locally cached rows for replies the server no longer lists
  // (e.g. removed on a federated server before this client ever saw a
  // live removal notice) — without this, a stale row flashes on load
  // before the server refresh corrects the count.
  async pruneStale(
    parentReedRef: string,
    liveReedIDs: Set<string>,
  ): Promise<void> {
    const cached = await reedRepliesRepository.listByParent(parentReedRef);
    for (const row of cached) {
      if (!liveReedIDs.has(row.reedID)) {
        await reedRepliesRepository.remove(row.reedID);
      }
    }
  },

  async listByParent(parentReedRef: string): Promise<ReedReplyRow[]> {
    return dbService.getAllByIndex<ReedReplyRow>('reedReplies', 'parentReedID', parentReedRef);
  },

  async remove(reedID: string): Promise<void> {
    await dbService.delete('reedReplies', reedID);
  },
};
