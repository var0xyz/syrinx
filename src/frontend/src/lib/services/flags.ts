import { writable } from 'svelte/store';
import type { MessageInitShape } from '@bufbuild/protobuf';
import type { ReedFlagSchema } from '$lib/proto/admin_pb';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildReedFlagPayload } from './signing';
import { signedAtHeader } from './verify';
import { dbService } from './db';
import { allowUnsigned } from '$lib/verifiers';

/** Bumped whenever the viewer flags a reed. */
export const flagsChanged = writable(0);

/** A reed the viewer flagged, with the signed flag a rebuilt server
 * gets back. */
export interface FlaggedReedRecord {
  reedID: string;
  flaggedAt: number;
  flag?: MessageInitShape<typeof ReedFlagSchema>;
}

export async function isFlagged(reedID: string): Promise<boolean> {
  return !!(await dbService.get<FlaggedReedRecord>('flaggedReeds', reedID));
}

async function recordFlagged(flag: MessageInitShape<typeof ReedFlagSchema>): Promise<void> {
  const record: FlaggedReedRecord = { reedID: flag.reedId!, flaggedAt: Date.now(), flag };
  await dbService.put('flaggedReeds', record, allowUnsigned);
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
  const flag = { reedId: reedID, reporterId: reporterID, note, createdAt, signature: { id: keyID, armor } };
  try {
    await apiService.flagReed(flag);
  } catch (err) {
    if ((err as { status?: number })?.status !== 409) throw err;
    await recordFlagged(flag);
    return false;
  }
  await recordFlagged(flag);
  return true;
}
