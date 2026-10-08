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
  /** Root of the conversation the reply belongs to. */
  rootId: string;
  /** Reply reed's own server-signed timestamp — sort/page key for the
   * cross-parent inbox view (replies page). */
  createdAt: string;
};

function rowFromFields(
  replyUserID: string,
  replyReedID: string,
  parentReedRef: string,
  rootId: string,
  createdAt: string,
): ReedReplyRow {
  return {
    reedID: replyReedID,
    userID: replyUserID,
    parentReedID: parentReedRef,
    rootId,
    createdAt,
  };
}

export const reedRepliesRepository = {
  async put(row: ReedReplyRow): Promise<void> {
    await dbService.put('reedReplies', row, allowUnsigned);
  },

  async upsertFromMeta(
    reply: api.ReplyMeta,
    parentReedRef: string,
    rootId: string,
  ): Promise<void> {
    await reedRepliesRepository.put(
      rowFromFields(reply.userID, reply.reedID, parentReedRef, rootId, reply.timestamp),
    );
  },

  async upsertFromReed(reed: Pick<ReedType, 'id' | 'userID' | 'replying' | 'serverSignature'>): Promise<void> {
    if (!reed.replying || !reed.serverSignature?.timestamp) return;
    await reedRepliesRepository.put(
      rowFromFields(reed.userID, reed.id, reed.replying.to, reed.replying.root, reed.serverSignature.timestamp),
    );
  },

  async syncFromServerList(
    parentReedRef: string,
    rootId: string,
    replies: api.ReplyMeta[],
  ): Promise<void> {
    for (const reply of replies) {
      await reedRepliesRepository.upsertFromMeta(reply, parentReedRef, rootId);
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

  async listByParent(parentReedRef: string, excludeUserID?: string): Promise<ReedReplyRow[]> {
    const rows = await dbService.getAllByIndex<ReedReplyRow>('reedReplies', 'parentReedID', parentReedRef);
    return excludeUserID ? rows.filter((row) => row.userID !== excludeUserID) : rows;
  },

  /** All locally known replies across every reed in parentReedRefs. */
  async listByParents(parentReedRefs: string[], excludeUserID?: string): Promise<ReedReplyRow[]> {
    const lists = await Promise.all(
      parentReedRefs.map((ref) => reedRepliesRepository.listByParent(ref, excludeUserID)),
    );
    return lists.flat();
  },

  /** Newest-first page of replies across every reed in parentReedRefs,
   * excluding excludeUserID's own replies (e.g. self-replies on your own
   * reed). Pass the previous page's last row's createdAt as `after` to
   * resume; omit for the first page. */
  async getPage(
    parentReedRefs: string[],
    excludeUserID: string,
    limit: number,
    after?: string,
  ): Promise<ReedReplyRow[]> {
    const parentSet = new Set(parentReedRefs);
    return dbService.getLatestFromIndex<ReedReplyRow>(
      'reedReplies',
      'createdAt',
      limit,
      (row) => parentSet.has(row.parentReedID) && row.userID !== excludeUserID,
      after,
    );
  },

  async remove(reedID: string): Promise<void> {
    await dbService.delete('reedReplies', reedID);
  },
};
