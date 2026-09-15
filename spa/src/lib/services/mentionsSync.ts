import { apiService } from './api';
import { serverConnection } from './serverConnection';
import { reedsService } from '$lib/repositories/reeds';
import { mentionsRepository } from '$lib/repositories/mentions';

const SYNC_PAGE_LIMIT = 50;

/**
 * Syncs GET /mentions into the local mentions table, resuming from the
 * latest createdAt already stored. For each new row, fetches the reed if
 * not already held (verified+stored by requestReedContent), then confirms
 * the claim is real before keeping it — a claim the content doesn't back
 * up is reported and dropped, not stored.
 */
export async function syncMentions(): Promise<{ added: number }> {
  const myUserID = localStorage.getItem('userId') ?? '';
  let cursor = await mentionsRepository.getLatestCreatedAt();
  let added = 0;

  for (;;) {
    const page = await apiService.getMentions({ before: cursor, limit: SYNC_PAGE_LIMIT });
    if (page.mentions.length === 0) break;

    for (const item of page.mentions) {
      let reed = await reedsService.getReed(item.reedID);
      if (!reed) {
        reed = await serverConnection.requestReedContent(item.reedID).catch(() => null);
      }
      if (!reed) {
        // Not yet available from any holder — leave for a later sync pass.
        continue;
      }
      if (!reed.mentions.includes(myUserID)) {
        await apiService.deleteMention(item.reedID, 'mention_claim_mismatch').catch(() => {});
        continue;
      }
      await mentionsRepository.put(item);
      added++;
    }

    cursor = page.mentions[page.mentions.length - 1].createdAt;
    if (!page.hasMore) break;
  }

  return { added };
}
