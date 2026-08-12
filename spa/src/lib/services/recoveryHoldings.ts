/**
 * Recovery phase 2–3: reed metadata, follow pages, and import complete.
 */

import type { ReedType } from '$lib/types/reed';
import { apiService } from './api';
import { dbService } from './db';

/** POST one reed from IndexedDB (must have server block + user signature).
 * Recovery only ever reports the current device's own account's reeds. */
export async function reportRecoveryReed(reedId: string): Promise<void> {
  await dbService.init();
  const ownUserId = localStorage.getItem('userId') ?? '';
  const reed = await dbService.get<ReedType>('reeds', [ownUserId, reedId]);
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

  await apiService.reportRecoveryReed({
    reedID: reed.id,
    authorID: reed.userID,
    userSignature: reed.userSignature,
    serverSignature: reed.serverSignature,
  });
}

/** POST one follow page (≤100 user IDs). */
export async function reportRecoveryFollowing(
  userIds: string[]
): Promise<void> {
  await apiService.reportRecoveryFollowing({ userIDs: userIds });
}

/** POST /recovery/complete (idempotent server-side). */
export async function completeRecoveryImport(): Promise<void> {
  await apiService.completeRecovery();
}
