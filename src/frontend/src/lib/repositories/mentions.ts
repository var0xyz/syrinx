import { dbService } from '$lib/services/db';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import type { ReedType } from '$lib/types/reed';
import type { User } from '$lib/types/api';

export interface MentionRecord {
  reedID: string;
  authorID: string;
  createdAt: string;
}

/** A local mention row resolved against its (already-held) reed + author,
 * ready to render — what getMentionItems returns. */
export interface ResolvedMentionItem {
  record: MentionRecord;
  reed: ReedType;
  author: User | { username: string };
}

// Server-authoritative claim metadata, not a signed cert — nothing to verify.
const noopVerifier = async () => true;

export const mentionsRepository = {
  async put(item: MentionRecord): Promise<void> {
    await dbService.put('mentions', item, noopVerifier);
  },

  async delete(reedID: string): Promise<void> {
    await dbService.delete('mentions', reedID);
  },

  /** Newest-first. */
  async getAll(): Promise<MentionRecord[]> {
    const all = await dbService.getAllSortedByIndex<MentionRecord>('mentions', 'createdAt');
    return all.reverse();
  },
};

/** Resolves every locally-stored mention against its reed + author,
 * dropping any whose reed isn't locally held. Newest-first. Shared by the
 * mentions route's load() (so the page has data before first render, no
 * loading flash) and MentionsList's own re-render after a background sync. */
export async function getMentionItems(): Promise<ResolvedMentionItem[]> {
  const records = await mentionsRepository.getAll();

  const resolved = await Promise.allSettled(
    records.map((record) => reedsService.getReed(record.reedID))
  );

  const withReeds: { record: MentionRecord; reed: ReedType }[] = [];
  records.forEach((record, i) => {
    const result = resolved[i];
    if (result.status === 'fulfilled' && result.value) {
      withReeds.push({ record, reed: result.value });
    }
  });

  const authorIds = [...new Set(withReeds.map((item) => item.reed.userID))];
  const authorResults = await Promise.allSettled(
    authorIds.map((id) => userRepository.getByUserId(id))
  );
  const authorMap = new Map<string, User>();
  authorIds.forEach((id, i) => {
    const result = authorResults[i];
    if (result.status === 'fulfilled' && result.value) {
      authorMap.set(id, result.value);
    }
  });

  return withReeds.map(({ record, reed }) => ({
    record,
    reed,
    author: authorMap.get(reed.userID) || { username: reed.userID },
  }));
}
