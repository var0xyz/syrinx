import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import type { ReedType } from '$lib/types/reed';

const STORE = 'tags';

/** One reed under one tag. createdAt is the reed's server timestamp, so a
 * tag pages by publication time rather than by author. */
export interface TagRow {
  name: string;
  reedID: string;
  createdAt: string;
}

export const tagsRepository = {
  /** Index a stored reed under each of its tags, lowercased. */
  async add(reed: ReedType): Promise<void> {
    const createdAt = reed.serverSignature?.timestamp;
    if (!createdAt) return;
    const names = new Set((reed.tags ?? []).map((tag) => tag.toLowerCase()));
    for (const name of names) {
      await dbService.put<TagRow>(STORE, { name, reedID: reed.id, createdAt }, allowUnsigned);
    }
  },

  /** Up to `limit` rows for a tag name, newest first, strictly before `after`. */
  async page(name: string, limit: number, after?: Pick<TagRow, 'createdAt' | 'reedID'>): Promise<TagRow[]> {
    // [] sorts after every string key, so [name, []] bounds all of its rows.
    const upper = after ? [name, after.createdAt, after.reedID] : [name, []];
    const range = IDBKeyRange.bound([name], upper, false, !!after);
    return dbService.getRangeFromIndex<TagRow>(STORE, 'byTime', range, limit);
  },

  /** Every tag any stored reed carries, ascending. */
  async names(): Promise<string[]> {
    return (await dbService.getUniqueIndexKeys(STORE, 'name')) as string[];
  },

  /** Drop a reed's rows when the reed itself is deleted. */
  async removeReed(reedID: string): Promise<void> {
    const rows = await dbService.getAllByIndex<TagRow>(STORE, 'reedID', reedID);
    await Promise.all(rows.map((row) => dbService.delete(STORE, [row.name, row.reedID])));
  },
};
