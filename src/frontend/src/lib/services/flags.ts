import { writable } from 'svelte/store';
import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildReedFlagPayload } from './signing';
import { dbService } from './db';
import { allowUnsigned, verifyOwnFlag } from '$lib/verifiers';

/** Bumped whenever the viewer flags a reed. */
export const flagsChanged = writable(0);

/** A reed the viewer flagged, with the countersigned flag a rebuilt server
 * gets back. A flag already raised elsewhere has none. */
export interface FlaggedReedRecord {
  reedID: string;
  flaggedAt: number;
  flag?: api.ReedFlag;
}

export async function isFlagged(reedID: string): Promise<boolean> {
  return !!(await dbService.get<FlaggedReedRecord>('flaggedReeds', reedID));
}

async function recordFlagged(reedID: string, flag?: api.ReedFlag): Promise<void> {
  const record: FlaggedReedRecord = { reedID, flaggedAt: Date.now(), flag };
  await dbService.put('flaggedReeds', record, (r) => (r.flag ? verifyOwnFlag(r.flag) : allowUnsigned(r)));
  flagsChanged.update((n) => n + 1);
}

/** Signs and sends a flag on reedID. False when it was already flagged,
 * from this device or another. */
export async function flagReed(reedID: string, note: string): Promise<boolean> {
  const reporterID = localStorage.getItem('userId');
  const keyID = authService.getActiveKeyId();
  if (!reporterID || !keyID) throw new Error('Not signed in');
  const armor = await requestSigner.sign(buildReedFlagPayload(reedID, reporterID, keyID, note));
  let countersigned: api.ReedFlag;
  try {
    countersigned = await apiService.flagReed({ reedId: reedID, reporterId: reporterID, note, signature: { id: keyID, armor } });
  } catch (err) {
    if ((err as { status?: number })?.status !== 409) throw err;
    await recordFlagged(reedID);
    return false;
  }
  await recordFlagged(reedID, countersigned);
  return true;
}
