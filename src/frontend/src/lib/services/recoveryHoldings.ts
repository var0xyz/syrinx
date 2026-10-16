/**
 * Recovery phase 2–3: reed metadata, follow pages, records, pins and
 * import complete.
 */

import type { ReedType } from '$lib/types/reed';
import type * as api from '$lib/types/api';
import { apiService } from './api';
import { dbService } from './db';
import { loadRecoveryRecords, type RecoveryRecordRef } from './recoveryRecords';

/** POST one reed from IndexedDB (must have server block + user signature).
 * Recovery only ever reports the current device's own account's reeds. */
export async function reportRecoveryReed(reedId: string): Promise<void> {
  await dbService.init();
  const reed = await dbService.get<ReedType>('reeds', reedId);
  if (!reed) {
    throw new Error(`Missing reed ${reedId}`);
  }
  if (!reed.serverSignature) {
    throw new Error(`Reed ${reedId} missing server countersignature`);
  }
  if (!reed.userSignature?.armor) {
    throw new Error(`Reed ${reedId} missing user signature`);
  }
  if (!reed.userID) {
    throw new Error(`Reed ${reedId} missing userID`);
  }

  // The links travel as the author signed them; the server can't check
  // them without the content, the same as when the reed was posted.
  await apiService.reportRecoveryReed({
    reedId: reed.id,
    userSignature: reed.userSignature,
    serverSignature: reed.serverSignature,
    replyingTo: reed.replying?.to,
    replyingRoot: reed.replying?.root,
    echoing: reed.echoing,
    threadHead: reed.thread?.head,
    threadIndex: reed.thread?.index,
    mentions: reed.mentions,
  });
}

/** POST one page of signed records; returns how many were refused. */
export async function reportRecoveryRecords(refs: RecoveryRecordRef[]): Promise<number> {
  await dbService.init();
  const records = await loadRecoveryRecords(refs);
  if (records.length === 0) return 0;
  const results = await apiService.reportRecoveryRecords(records);
  const refused = results.filter((r) => r.status === 'refused');
  for (const r of refused) console.warn('recovery record refused:', r.reason);
  return refused.length;
}

/** POST the caller's pinned reeds, as their cached profile hints list them. */
export async function reportRecoveryPins(selfUserId: string): Promise<void> {
  await dbService.init();
  const info = await dbService.get<api.UserInfo>('usersInfo', selfUserId);
  if (!info?.pinnedReedIds?.length) return;
  await apiService.reportRecoveryPins([...info.pinnedReedIds]);
}

/** POST one follow page (≤100 user IDs). */
export async function reportRecoveryFollowing(
  userIds: string[]
): Promise<void> {
  await apiService.reportRecoveryFollowing(userIds);
}

/** POST /recovery/complete (idempotent server-side). */
export async function completeRecoveryImport(): Promise<void> {
  await apiService.completeRecovery();
}
