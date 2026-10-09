import { dbService } from '$lib/services/db';
import type { ReedType } from '$lib/types/reed';

export interface LocalSearchPage<T, Cursor = string> {
  items: T[];
  hasMore: boolean;
  nextCursor?: Cursor;
}

function normalize(text: string): string {
  return text.toLowerCase();
}

/** Reed search is entirely local — over reeds already synced to this
 * device's IndexedDB, never a server-side query. */
export const localSearchRepository = {
  /** Walks reeds newest-first (serverSignature.signedAt), filtering by a
   * case-insensitive content substring match. Pages via the same
   * fetch-one-extra trick as likedReedsRepository.getPage. */
  async searchReeds(query: string, limit: number, after?: number): Promise<LocalSearchPage<ReedType, number>> {
    const needle = normalize(query.trim());
    if (!needle) return { items: [], hasMore: false };

    const matched = await dbService.getLatestFromIndex<ReedType>(
      'reeds',
      'serverSignature.signedAt',
      limit + 1,
      (reed) => normalize(reed.content || '').includes(needle),
      after,
    );

    const hasMore = matched.length > limit;
    const items = matched.slice(0, limit);
    const last = items[items.length - 1];
    const nextCursor = last?.serverSignature?.signedAt ?? after;
    return { items, hasMore, nextCursor };
  },
};
