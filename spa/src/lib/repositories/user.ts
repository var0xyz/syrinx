import { apiService } from '$lib/services/api';
import { dbService } from '$lib/services/db';
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
    return user;
  }
}

export const userRepository = new UserRepository();
