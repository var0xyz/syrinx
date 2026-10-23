import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { authService } from '$lib/services/auth';
import { requestSigner } from '$lib/services/request-signer';
import { buildFollowUserPayload } from '$lib/services/signing';
import { allowUnsigned, verifyOwnFollow } from '$lib/verifiers';
import { userListsRepository } from '$lib/repositories/userLists';
import type * as api from '$lib/types/api';

interface FollowRecord {
  userId: string;
  timestamp: number;
}

/** A signed follow or unfollow waiting to reach the server. */
interface PendingFollowRecord {
  userId: string;
  timestamp: number;
  keyId: string;
  signature: string;
}

const FOLLOWING_STORE = 'following';
const FOLLOW_CERTS_STORE = 'followCerts';
const PENDING_FOLLOWS_STORE = 'pendingFollows';
const UNFOLLOW_STORE = 'unfollow';

/** A refusal that retrying can't fix. */
function isPermanent(error: unknown): boolean {
  const status = (error as { status?: number })?.status;
  return status != null && status >= 400 && status < 500 && status !== 401 && status !== 429;
}

async function sign(userId: string, follow: boolean): Promise<PendingFollowRecord> {
  const viewerID = localStorage.getItem('userId');
  const keyId = authService.getActiveKeyId();
  if (!viewerID || !keyId) throw new Error('Not signed in');
  const signature = await requestSigner.sign(buildFollowUserPayload(viewerID, userId, keyId, follow));
  return { userId, timestamp: Date.now(), keyId, signature };
}

/** Keeps the countersigned certificate, the latest per followee. */
async function keepCert(cert: api.FollowCert): Promise<void> {
  try {
    await dbService.put(FOLLOW_CERTS_STORE, cert, verifyOwnFollow);
  } catch (error) {
    console.warn('[following] refused follow certificate', cert.followeeId, error);
  }
}

async function flush(record: PendingFollowRecord, follow: boolean): Promise<void> {
  const store = follow ? PENDING_FOLLOWS_STORE : UNFOLLOW_STORE;
  try {
    const cert = follow
      ? await apiService.followUser(record.userId, record.signature, record.keyId)
      : await apiService.unfollowUser(record.userId, record.signature, record.keyId);
    await keepCert(cert);
    await dbService.delete(store, record.userId);
  } catch (error) {
    console.error(`Failed to sync ${follow ? 'follow' : 'unfollow'} to server:`, error);
    if (!isPermanent(error)) return;
    await dbService.delete(store, record.userId);
    if (follow) await dbService.delete(FOLLOWING_STORE, record.userId);
  }
}

export const followingRepository = {
  async isFollowing(userId: string): Promise<boolean> {
    const record = await dbService.get<FollowRecord>(FOLLOWING_STORE, userId);
    return !!record;
  },

  /** Records a follow the server already holds, with its certificate. */
  async recordLocalFollow(cert: api.FollowCert): Promise<void> {
    await keepCert(cert);
    if (await dbService.get<FollowRecord>(FOLLOWING_STORE, cert.followeeId)) return;
    await dbService.put<FollowRecord>(
      FOLLOWING_STORE,
      { userId: cert.followeeId, timestamp: Date.now() },
      allowUnsigned
    );
  },

  async follow(userId: string): Promise<void> {
    if (await dbService.get<FollowRecord>(FOLLOWING_STORE, userId)) return;
    const record = await sign(userId, true);
    await dbService.delete(UNFOLLOW_STORE, userId);
    await dbService.put<FollowRecord>(FOLLOWING_STORE, { userId, timestamp: record.timestamp }, allowUnsigned);
    await dbService.put<PendingFollowRecord>(PENDING_FOLLOWS_STORE, record, allowUnsigned);
    await flush(record, true);
  },

  async unfollow(userId: string): Promise<void> {
    const record = await sign(userId, false);
    await dbService.delete(FOLLOWING_STORE, userId);
    await dbService.delete(PENDING_FOLLOWS_STORE, userId);
    await userListsRepository.removeMember(userId);
    await dbService.put<PendingFollowRecord>(UNFOLLOW_STORE, record, allowUnsigned);
    await flush(record, false);
  },

  async syncPending(): Promise<void> {
    for (const record of await dbService.getAll<PendingFollowRecord>(PENDING_FOLLOWS_STORE)) {
      await flush(record, true);
    }
    for (const record of await dbService.getAll<PendingFollowRecord>(UNFOLLOW_STORE)) {
      await flush(record, false);
    }
  },
};
