import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';

interface FollowRecord {
  userId: string;
  timestamp: number;
}

interface UnfollowRecord {
  userId: string;
  timestamp: number;
}

const FOLLOWING_STORE = 'following';
const PENDING_FOLLOWS_STORE = 'pendingFollows';
const UNFOLLOW_STORE = 'unfollow';

export const followingRepository = {
  async isFollowing(userId: string): Promise<boolean> {
    const record = await dbService.get<FollowRecord>(FOLLOWING_STORE, userId);
    return record !== null;
  },

  async follow(userId: string): Promise<void> {
    const existing = await dbService.get<FollowRecord>(FOLLOWING_STORE, userId);
    if (existing) return;

    const record: FollowRecord = { userId, timestamp: Date.now() };
    await dbService.put<FollowRecord>(FOLLOWING_STORE, record);
    await dbService.put<FollowRecord>(PENDING_FOLLOWS_STORE, record);

    try {
      await apiService.followUser(userId);
      await dbService.delete(PENDING_FOLLOWS_STORE, userId);
    } catch (error) {
      console.error('Failed to sync follow to server:', error);
    }
  },

  async unfollow(userId: string): Promise<void> {
    await dbService.delete(FOLLOWING_STORE, userId);
    await dbService.put<UnfollowRecord>(UNFOLLOW_STORE, { userId, timestamp: Date.now() });

    try {
      await apiService.unfollowUser(userId);
      await dbService.delete(UNFOLLOW_STORE, userId);
    } catch (error) {
      console.error('Failed to sync unfollow to server:', error);
    }
  },

  async getPendingFollows(): Promise<FollowRecord[]> {
    return dbService.getAll<FollowRecord>(PENDING_FOLLOWS_STORE);
  },

  async getPendingUnfollows(): Promise<UnfollowRecord[]> {
    return dbService.getAll<UnfollowRecord>(UNFOLLOW_STORE);
  },

  async syncPending(): Promise<void> {
    const pendingFollows = await dbService.getAll<FollowRecord>(PENDING_FOLLOWS_STORE);
    for (const record of pendingFollows) {
      try {
        await apiService.followUser(record.userId);
        await dbService.delete(PENDING_FOLLOWS_STORE, record.userId);
      } catch (error) {
        console.error('Failed to sync pending follow:', record.userId, error);
      }
    }

    const pendingUnfollows = await dbService.getAll<UnfollowRecord>(UNFOLLOW_STORE);
    for (const record of pendingUnfollows) {
      try {
        await apiService.unfollowUser(record.userId);
        await dbService.delete(UNFOLLOW_STORE, record.userId);
      } catch (error) {
        console.error('Failed to sync pending unfollow:', record.userId, error);
      }
    }
  },
};
