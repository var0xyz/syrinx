import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import type * as api from '$lib/types/api';
import type * as db from '$lib/types/db';

export class UserRepository {
  private readonly storeName = 'users';

  async get(userId: string): Promise<db.User | null> {
    return await dbService.get<db.User>(this.storeName, userId);
  }

  async put(user: api.User): Promise<db.User> {
    return await dbService.put<api.User, db.User>(this.storeName, user);
  }

  async delete(userId: string): Promise<void> {
    await dbService.delete(this.storeName, userId);
  }

  async fetchAndStore(userId: string): Promise<db.User> {
    const user: api.User = await apiService.getUser(userId);
    return await this.put(user);
  }

  async getByUserId(userId: string): Promise<db.User> {
    const cached: db.User = await this.get(userId);

    if (cached) {
      return cached;
    }

    // No cached data, fetch from server
    try {
      return await this.fetchAndStore(userId);
    } catch (error) {
      console.error('Error fetching user from server:', error);
      return null;
    }
  }
}

export const userRepository = new UserRepository();
