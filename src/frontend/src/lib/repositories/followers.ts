import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyFollowOfViewer } from '$lib/verifiers';

/** Verified follows of the viewer, the latest per follower: kept for
 * recovery, never removed by the user or by eviction. */
export const followersRepository = {
  async put(cert: api.FollowCert): Promise<void> {
    await dbService.put('followers', cert, verifyFollowOfViewer);
  },

  async get(followerID: string): Promise<api.FollowCert | null> {
    return dbService.get<api.FollowCert>('followers', followerID);
  },

  async getAll(): Promise<api.FollowCert[]> {
    return dbService.getAll<api.FollowCert>('followers');
  },

  async delete(followerID: string): Promise<void> {
    await dbService.delete('followers', followerID);
  },
};
