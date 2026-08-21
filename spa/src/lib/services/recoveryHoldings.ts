/**
 * Recovery phase 2–3: reed metadata, follow pages, and import complete.
 */

import type { ReedType } from '$lib/types/reed';
import { apiService } from './api';
import { dbService } from './db';
import { parseKeyId } from '$lib/utils/keyId';

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

  // recovery/wire.go's ReedRequest is a deliberate bare-everything
  // exception (recoveryKeyNest.ts signs bare payloads) — send the bare
  // suffix, not the canonical reed.id.
  const bareReedID = parseKeyId(reed.id)?.fingerprint ?? reed.id;
  await apiService.reportRecoveryReed({
    reedID: bareReedID,
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
