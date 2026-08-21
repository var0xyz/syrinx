import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { buildReedRemovalUserPayload } from './signing';
import { pendingRemovalRepository } from '$lib/repositories/pendingRemoval';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { verifyReedRemoval } from '$lib/verifiers';
import { get, writable } from 'svelte/store';
import { serverInfo } from './serverInfo';
import { refForReed } from '$lib/utils/reedRef';

export { verifyReedRemoval };

/** Incremented after each reed removal cert is committed locally — lets an
 * open view (e.g. the reed detail page's conversation thread) know to
 * reload rather than keep showing a reply that just got deleted. */
export const reedRemovalCommitted = writable(0);

/**
 * Persist cert then drop the local reed. Verification runs inside
 * `removedReedsRepository.put`.
 */
export async function commitReedRemovalLocally(cert: api.ReedRemoval): Promise<void> {
  await removedReedsRepository.put(cert);
  await dbService.delete('reeds', cert.reedID);
}

/** Put-then-side-effects. Returns false if verification fails (reed retained). */
export async function verifyAndCommitReedRemoval(cert: api.ReedRemoval): Promise<boolean> {
  try {
    await commitReedRemovalLocally(cert);
    reedRemovalCommitted.update((n) => n + 1);
    return true;
  } catch (error) {
    console.error('[verifyAndCommitReedRemoval] refused', cert?.reedID, error);
    return false;
  }
}

/**
 * Author path: queue pending → DELETE → verify countersig →
 * removedReeds → delete reed → clear pending.
 */
export async function removeReedAsAuthor(reedID: string): Promise<api.ReedRemoval> {
  const info = get(serverInfo);
  const serverID = info?.id || localStorage.getItem('serverId');
  if (!serverID) {
    throw new Error('Server ID not available');
  }

  const fingerprint = authService.getActiveKeyFingerprint();
  const passphrase = authService.getPassphrase();
  if (!fingerprint || !passphrase) {
    throw new Error('Active key or passphrase not available');
  }

  const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
  if (!privateKey?.armor) {
    throw new Error('Private key not found');
  }

  const userPayload = buildReedRemovalUserPayload(serverID, reedID);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  await pendingRemovalRepository.put({
    reedID,
    serverID,
    signature,
  });

  const cert = await apiService.deleteReed(reedID, signature);
  if (!(await verifyAndCommitReedRemoval(cert))) {
    throw new Error('Server reed-removal countersignature failed verification');
  }
  await pendingRemovalRepository.delete(reedID);
  return cert;
}
