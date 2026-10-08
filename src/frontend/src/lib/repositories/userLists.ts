import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { generateUserListId } from '$lib/utils/id';
import type { UserListType } from '$lib/types/userList';

export const USER_LIST_NAME_MAX = 32;
export const USER_LIST_DESCRIPTION_MAX = 140;
const USER_LISTS_STORE = 'userLists';

export interface UserListInput {
  name: string;
  description: string;
  memberIds: string[];
}

function validate(name: string, description: string): void {
  if (!name) throw new Error('List name is required');
  if (name.length > USER_LIST_NAME_MAX) {
    throw new Error(`List name cannot exceed ${USER_LIST_NAME_MAX} characters`);
  }
  if (description.length > USER_LIST_DESCRIPTION_MAX) {
    throw new Error(`Description cannot exceed ${USER_LIST_DESCRIPTION_MAX} characters`);
  }
}

export const userListsRepository = {
  async getAll(): Promise<UserListType[]> {
    const all = await dbService.getAll<UserListType>(USER_LISTS_STORE);
    return all.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
  },

  async get(id: string): Promise<UserListType | null> {
    return dbService.get<UserListType>(USER_LISTS_STORE, id);
  },

  /** Case-insensitive uniqueness check; pass excludeId when editing so a
   * list doesn't collide with its own unchanged name. */
  async isNameTaken(name: string, excludeId?: string): Promise<boolean> {
    const all = await dbService.getAll<UserListType>(USER_LISTS_STORE);
    const normalized = name.trim().toLowerCase();
    return all.some((l) => l.id !== excludeId && l.name.trim().toLowerCase() === normalized);
  },

  async create(input: UserListInput): Promise<UserListType> {
    const name = input.name.trim();
    const description = input.description.trim();
    validate(name, description);
    if (await this.isNameTaken(name)) {
      throw new Error('A list with this name already exists');
    }

    const userList: UserListType = {
      id: generateUserListId(),
      name,
      description,
      memberIds: [...new Set(input.memberIds)],
      createdAt: Date.now(),
    };
    await dbService.put<UserListType>(USER_LISTS_STORE, userList, allowUnsigned);
    return userList;
  },

  async update(id: string, input: UserListInput): Promise<UserListType> {
    const existing = await this.get(id);
    if (!existing) throw new Error('List not found');

    const name = input.name.trim();
    const description = input.description.trim();
    validate(name, description);
    if (await this.isNameTaken(name, id)) {
      throw new Error('A list with this name already exists');
    }

    const updated: UserListType = {
      ...existing,
      name,
      description,
      memberIds: [...new Set(input.memberIds)],
    };
    await dbService.put<UserListType>(USER_LISTS_STORE, updated, allowUnsigned);
    return updated;
  },

  async delete(id: string): Promise<void> {
    await dbService.delete(USER_LISTS_STORE, id);
  },

  /** Lists only hold people you follow, so an unfollow takes them off all. */
  async removeMember(userID: string): Promise<void> {
    for (const userList of await dbService.getAll<UserListType>(USER_LISTS_STORE)) {
      if (!userList.memberIds.includes(userID)) continue;
      const memberIds = userList.memberIds.filter((id) => id !== userID);
      await dbService.put<UserListType>(USER_LISTS_STORE, { ...userList, memberIds }, allowUnsigned);
    }
  },
};
