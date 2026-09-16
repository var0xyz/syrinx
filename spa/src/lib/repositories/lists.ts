import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { generateListId } from '$lib/utils/id';
import type { ListType } from '$lib/types/list';

export const LIST_NAME_MAX = 32;
export const LIST_DESCRIPTION_MAX = 140;
const LISTS_STORE = 'lists';

export interface ListInput {
  name: string;
  description: string;
  memberIds: string[];
}

function validate(name: string, description: string): void {
  if (!name) throw new Error('List name is required');
  if (name.length > LIST_NAME_MAX) {
    throw new Error(`List name cannot exceed ${LIST_NAME_MAX} characters`);
  }
  if (description.length > LIST_DESCRIPTION_MAX) {
    throw new Error(`Description cannot exceed ${LIST_DESCRIPTION_MAX} characters`);
  }
}

export const listsRepository = {
  async getAll(): Promise<ListType[]> {
    const all = await dbService.getAll<ListType>(LISTS_STORE);
    return all.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
  },

  async get(id: string): Promise<ListType | null> {
    return dbService.get<ListType>(LISTS_STORE, id);
  },

  /** Case-insensitive uniqueness check; pass excludeId when editing so a
   * list doesn't collide with its own unchanged name. */
  async isNameTaken(name: string, excludeId?: string): Promise<boolean> {
    const all = await dbService.getAll<ListType>(LISTS_STORE);
    const normalized = name.trim().toLowerCase();
    return all.some((l) => l.id !== excludeId && l.name.trim().toLowerCase() === normalized);
  },

  async create(input: ListInput): Promise<ListType> {
    const name = input.name.trim();
    const description = input.description.trim();
    validate(name, description);
    if (await this.isNameTaken(name)) {
      throw new Error('A list with this name already exists');
    }

    const list: ListType = {
      id: generateListId(),
      name,
      description,
      memberIds: [...new Set(input.memberIds)],
      createdAt: Date.now(),
    };
    await dbService.put<ListType>(LISTS_STORE, list, allowUnsigned);
    return list;
  },

  async update(id: string, input: ListInput): Promise<ListType> {
    const existing = await this.get(id);
    if (!existing) throw new Error('List not found');

    const name = input.name.trim();
    const description = input.description.trim();
    validate(name, description);
    if (await this.isNameTaken(name, id)) {
      throw new Error('A list with this name already exists');
    }

    const updated: ListType = {
      ...existing,
      name,
      description,
      memberIds: [...new Set(input.memberIds)],
    };
    await dbService.put<ListType>(LISTS_STORE, updated, allowUnsigned);
    return updated;
  },

  async delete(id: string): Promise<void> {
    await dbService.delete(LISTS_STORE, id);
  },
};
