import { dbService } from '$lib/services/db';
import type { ReedType } from '$lib/types/reed';
import type * as api from '$lib/types/api';

export interface LocalSearchPage<T> {
  items: T[];
  hasMore: boolean;
  nextCursor?: string;
}

function normalize(text: string): string {
  return text.toLowerCase();
}

/** Search is entirely local — over reeds/users already synced to this
 * device's IndexedDB, never a server-side query. */
export const localSearchRepository = {
  /** Walks reeds newest-first (serverSignature.timestamp), filtering by a
   * case-insensitive content substring match. Pages via the same
   * fetch-one-extra trick as likedReedsRepository.getPage. */
  async searchReeds(query: string, limit: number, after?: string): Promise<LocalSearchPage<ReedType>> {
    const needle = normalize(query.trim());
    if (!needle) return { items: [], hasMore: false };

    const matched = await dbService.getLatestFromIndex<ReedType>(
      'reeds',
      'serverSignature.timestamp',
      limit + 1,
      (reed) => normalize(reed.content || '').includes(needle),
      after,
    );

    const hasMore = matched.length > limit;
    const items = matched.slice(0, limit);
    const last = items[items.length - 1];
    const nextCursor = last?.serverSignature?.timestamp ?? after;
    return { items, hasMore, nextCursor };
  },

  /** users is a small local profile cache (every user this device has
   * ever seen), so this reads it whole and paginates in memory rather
   * than needing a dedicated index/cursor. */
  async searchUsers(query: string, limit: number, after?: string): Promise<LocalSearchPage<api.User>> {
    const needle = normalize(query.trim());
    if (!needle) return { items: [], hasMore: false };

    const all = await dbService.getAll<api.User>('users');
    const matched = all
      .filter((user) => !(user as any).__meta__?.deleted)
      .filter((user) =>
        normalize(user.username || '').includes(needle) || normalize(user.bio || '').includes(needle)
      )
      .sort((a, b) => a.username.localeCompare(b.username));

    const start = after ? matched.findIndex((u) => u.id === after) + 1 : 0;
    const page = matched.slice(start, start + limit);
    const hasMore = start + limit < matched.length;
    const nextCursor = page[page.length - 1]?.id ?? after;
    return { items: page, hasMore, nextCursor };
  },
};
