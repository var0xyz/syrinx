/**
 * Recovery phase 2–3: reed metadata, follow pages, and import complete.
 */

import type { ReedType } from '$lib/types/reed';
import { apiService } from './api';
import { dbService } from './db';

/** POST one reed from IndexedDB (must have server block + user signature). */
export async function reportRecoveryReed(reedId: string): Promise<void> {
  await dbService.init();
  const reed = await dbService.get<ReedType>('reeds', reedId);
  if (!reed) {
    throw new Error(`Missing reed ${reedId}`);
  }
  if (!reed.server) {
    throw new Error(`Reed ${reedId} missing server countersignature`);
  }
  if (!reed.signature) {
    throw new Error(`Reed ${reedId} missing user signature`);
  }
  if (!reed.headers?.author) {
    throw new Error(`Reed ${reedId} missing author`);
  }

  await apiService.reportRecoveryReed({
    reedID: reed.headers.id,
    authorID: reed.headers.author,
    userSignature: reed.signature,
    server: reed.server,
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
