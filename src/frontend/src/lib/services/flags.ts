import { writable } from 'svelte/store';
import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildReedFlagPayload } from './signing';
import { flagsRepository } from '$lib/repositories/flags';

/** Bumped whenever the viewer flags a reed. */
export const flagsChanged = writable(0);

export async function isFlagged(reedID: string): Promise<boolean> {
  return flagsRepository.isFlaggedByViewer(reedID);
}

/** Signs and sends a flag on reedID, and keeps it countersigned. False
 * when it was already flagged, from this device or another. */
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
    await flagsRepository.putFlag({ reedId: reedID, reporterId: reporterID });
    flagsChanged.update((n) => n + 1);
    return false;
  }
  await flagsRepository.putFlag(countersigned);
  flagsChanged.update((n) => n + 1);
  return true;
}
