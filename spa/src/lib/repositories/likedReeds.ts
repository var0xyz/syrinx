import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyReedLike } from '$lib/verifiers';

export interface LikedReedRecord extends api.ReedLike {
  compositeKey: string; // `${authorID}:${reedID}`
  likedAt: string; // server.timestamp, for newest-liked-first ordering
}

function compositeKey(authorID: string, reedID: string): string {
  return `${authorID}:${reedID}`;
}

export const likedReedsRepository = {
  async put(cert: api.ReedLike): Promise<void> {
    const record: LikedReedRecord = {
      ...cert,
      compositeKey: compositeKey(cert.authorID, cert.reedID),
      likedAt: cert.serverSignature.timestamp,
    };
    await dbService.put('likedReeds', record, verifyReedLike);
  },

  async delete(authorID: string, reedID: string): Promise<void> {
    await dbService.delete('likedReeds', compositeKey(authorID, reedID));
  },

  async get(authorID: string, reedID: string): Promise<LikedReedRecord | null> {
    return dbService.get<LikedReedRecord>('likedReeds', compositeKey(authorID, reedID));
  },

  async has(authorID: string, reedID: string): Promise<boolean> {
    return !!(await likedReedsRepository.get(authorID, reedID));
  },

  /** Newest-liked-first. */
  async getAll(): Promise<LikedReedRecord[]> {
    const all = await dbService.getAllSortedByIndex<LikedReedRecord>('likedReeds', 'likedAt');
    return all.reverse();
  },
};
