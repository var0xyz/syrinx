import { writable } from 'svelte/store';
import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildFlagWithdrawalPayload, buildReedFlagPayload } from './signing';
import { flagsRepository, type StoredFlag } from '$lib/repositories/flags';

/** Bumped whenever the viewer flags a reed or withdraws a flag. */
export const flagsChanged = writable(0);

/** The viewer's own flag on reedID as this device holds it, if any. */
export async function ownFlag(reedID: string): Promise<StoredFlag | null> {
  return flagsRepository.ownFlag(reedID);
}

/** Fetches and keeps the viewer's countersigned flag on reedID, for a
 * device that knows of it only from a 409. */
export async function fetchOwnFlag(reedID: string): Promise<api.ReedFlag> {
  const flag = await apiService.getOwnFlag(reedID);
  await flagsRepository.putFlag(flag);
  flagsChanged.update((n) => n + 1);
  return flag;
}

/** Signs and sends a withdrawal of the viewer's flag on reedID, and keeps
 * the withdrawn flag. Throws flag-acknowledged once admins acted on it. */
export async function withdrawFlag(reedID: string): Promise<api.ReedFlag> {
  const reporterID = localStorage.getItem('userId');
  const keyID = authService.getActiveKeyId();
  if (!reporterID || !keyID) throw new Error('Not signed in');
  let held = await flagsRepository.ownFlag(reedID);
  // A flag known only from a 409 needs its signature from the server.
  if (!held?.signature?.armor) held = await fetchOwnFlag(reedID);
  if (held.withdrawal) return held as api.ReedFlag;
  const armor = await requestSigner.sign(buildFlagWithdrawalPayload(reedID, reporterID, keyID, held.signature?.armor ?? ''));
  const withdrawn = await apiService.withdrawFlag(reedID, { signature: { id: keyID, armor } });
  await flagsRepository.putFlag(withdrawn);
  flagsChanged.update((n) => n + 1);
  return withdrawn;
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
