import { apiService } from './api';
import { serverConnection } from './serverConnection';
import { reedsService } from '$lib/repositories/reeds';
import { mentionsRepository, type MentionRecord } from '$lib/repositories/mentions';

/** Confirms item.reedID actually mentions the caller before storing it —
 * an unbacked claim is reported (DeleteMention) and dropped. Called from
 * the live MENTION push, both direct and catch-up-on-reconnect. */
export async function verifyAndStoreMention(item: MentionRecord): Promise<boolean> {
  const myUserID = localStorage.getItem('userId') ?? '';

  let reed = await reedsService.getReed(item.reedID);
  if (!reed) {
    reed = await serverConnection.requestReedContent(item.reedID).catch(() => null);
  }
  if (!reed) {
    return false;
  }
  if (!reed.mentions.includes(myUserID)) {
    await apiService.deleteMention(item.reedID, 'mention_claim_mismatch').catch(() => {});
    return false;
  }
  await mentionsRepository.put(item);
  return true;
}
