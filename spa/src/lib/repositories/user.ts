import { apiService } from '$lib/services/api';
import { dbService } from '$lib/services/db';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import type * as api from '$lib/types/api';

export class UserRepository {
  private readonly storeName = 'users';

  async get(userId: string): Promise<api.User | null> {
    return await dbService.get<api.User>(this.storeName, userId);
  }

  async put(user: api.User): Promise<void> {
    await dbService.put<api.User>(this.storeName, user);
  }

  async delete(userId: string): Promise<void> {
    await dbService.delete(this.storeName, userId);
  }

  async isTombstone(userId: string): Promise<boolean> {
    const record = await this.get(userId);
    return !!(record as any)?.__meta__?.deleted;
  }

  async writeTombstone(userId: string): Promise<void> {
    await dbService.put(this.storeName, {
      id: userId,
      __meta__: { deleted: true, timestamp: Date.now() }
    } as any);
  }

  async getByUserId(userId: string): Promise<api.User> {
    let user: api.User = await this.get(userId);

    if (user) {
      return user;
    }

    // No cached data, fetch from server
    try {
      user = await apiService.getUser(userId);
    } catch (error) {
      console.error('Error fetching user from server:', error);
      return null;
    }

    await this.put(user);

    if (user.fingerprint && !(await publicKeyRepository.hasPublicKey(user.fingerprint))) {
      try {
        const key = await apiService.getPublicKey(userId, user.fingerprint);
        await publicKeyRepository.put(key.fingerprint, key.armor);
      } catch (error) {
        console.error('Error fetching public key for user:', error);
      }
    }

    return user;
  }
}

export const userRepository = new UserRepository();
