import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyReedLike } from '$lib/verifiers';

export interface LikedReedRecord extends api.ReedLike {
  compositeKey: string; // reedRef (authorID@serverID/uuid)
  likedAt: string; // server.timestamp, for newest-liked-first ordering
}

export const likedReedsRepository = {
  async put(cert: api.ReedLike): Promise<void> {
    const record: LikedReedRecord = {
      ...cert,
      compositeKey: cert.reedID,
      likedAt: cert.serverSignature.timestamp,
    };
    await dbService.put('likedReeds', record, verifyReedLike);
  },

  async delete(reedRef: string): Promise<void> {
    await dbService.delete('likedReeds', reedRef);
  },

  async get(reedRef: string): Promise<LikedReedRecord | null> {
    return dbService.get<LikedReedRecord>('likedReeds', reedRef);
  },

  async has(reedRef: string): Promise<boolean> {
    return !!(await likedReedsRepository.get(reedRef));
  },

  /** Newest-liked-first page. Pass the previous page's last record's
   * likedAt as `after` to resume; omit for the first page. */
  async getPage(limit: number, after?: string): Promise<LikedReedRecord[]> {
    return dbService.getLatestFromIndex<LikedReedRecord>('likedReeds', 'likedAt', limit, undefined, after);
  },
};
