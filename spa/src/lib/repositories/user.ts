import { apiService } from '$lib/services/api';
import { dbService } from '$lib/services/db';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { allowUnsigned, verifyUser } from '$lib/verifiers';
import type * as api from '$lib/types/api';

export class UserRepository {
  private readonly storeName = 'users';

  async get(userId: string): Promise<api.User | null> {
    return await dbService.get<api.User>(this.storeName, userId);
  }

  async put(user: api.User): Promise<void> {
    await dbService.put<api.User>(this.storeName, user, verifyUser);
  }

  async delete(userId: string): Promise<void> {
    await dbService.delete(this.storeName, userId);
  }

  async isTombstone(userId: string): Promise<boolean> {
    const record = await this.get(userId);
    return !!(record as any)?.__meta__?.deleted;
  }

  async writeTombstone(userId: string): Promise<void> {
    await dbService.put(
      this.storeName,
      {
        id: userId,
        __meta__: { deleted: true, timestamp: Date.now() }
      } as any,
      allowUnsigned
    );
  }

  async getByUserId(userId: string): Promise<api.User> {
    let user: api.User = await this.get(userId);

    if (user) {
      return user;
    }

    try {
      user = await apiService.getUser(userId);
    } catch (error) {
      console.error('Error fetching user from server:', error);
      return null;
    }

    await this.put(user);

    if (
      user.userSignature?.fingerprint &&
      !(await publicKeyRepository.hasPublicKey(user.userSignature.fingerprint))
    ) {
      try {
        const key = await apiService.getPublicKey(userId, user.userSignature.fingerprint);
        await publicKeyRepository.put(key);
      } catch (error) {
        console.error('Error fetching public key for user:', error);
      }
    }

    return user;
  }
}

export const userRepository = new UserRepository();
