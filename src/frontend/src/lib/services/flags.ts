import { writable } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildReedFlagPayload } from './signing';
import { signedAtHeader } from './verify';
import { dbService } from './db';
import { allowUnsigned } from '$lib/verifiers';

/** Bumped whenever the viewer flags a reed. */
export const flagsChanged = writable(0);

export async function isFlagged(reedID: string): Promise<boolean> {
  return !!(await dbService.get<{ reedID: string }>('flaggedReeds', reedID));
}

async function recordFlagged(reedID: string): Promise<void> {
  await dbService.put('flaggedReeds', { reedID, flaggedAt: Date.now() }, allowUnsigned);
  flagsChanged.update((n) => n + 1);
}

/** Signs and sends a flag on reedID. False when it was already flagged,
 * from this device or another. */
export async function flagReed(reedID: string, note: string): Promise<boolean> {
  const reporterID = localStorage.getItem('userId');
  const keyID = authService.getActiveKeyId();
  if (!reporterID || !keyID) throw new Error('Not signed in');
  const createdAt = Math.floor(Date.now() / 1000);
  const armor = await requestSigner.sign(
    buildReedFlagPayload(reedID, reporterID, keyID, note, signedAtHeader(createdAt))
  );
  try {
    await apiService.flagReed({ reedId: reedID, reporterId: reporterID, note, createdAt, signature: { id: keyID, armor } });
  } catch (err) {
    if ((err as { status?: number })?.status !== 409) throw err;
    await recordFlagged(reedID);
    return false;
  }
  await recordFlagged(reedID);
  return true;
}
