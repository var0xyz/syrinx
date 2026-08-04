import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import type * as api from '$lib/types/api';

/**
 * Local cache of unsigned user hints (GET /users/{id}/info).
 * Always overwritten with whatever the server last sent — no verify.
 */
export class UserInfoRepository {
  private readonly storeName = 'usersInfo';

  async get(userId: string): Promise<api.UserInfo | null> {
    return await dbService.get<api.UserInfo>(this.storeName, userId);
  }

  async put(info: api.UserInfo): Promise<void> {
    await dbService.put<api.UserInfo>(this.storeName, info, allowUnsigned);
  }

  async delete(userId: string): Promise<void> {
    await dbService.delete(this.storeName, userId);
  }
}

export const userInfoRepository = new UserInfoRepository();
