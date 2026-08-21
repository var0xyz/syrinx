import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { buildReedLikeUserPayload } from './signing';
import { pendingLikeRepository } from '$lib/repositories/pendingLike';
import { pendingUnlikeRepository } from '$lib/repositories/pendingUnlike';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { likedReedsRepository } from '$lib/repositories/likedReeds';
import { verifyReedLike } from '$lib/verifiers';
import { get } from 'svelte/store';
import { serverInfo } from './serverInfo';
import { refForReed } from '$lib/utils/reedRef';

export { verifyReedLike };

/**
 * Persist cert then flip local like state. Verification runs inside
 * `likedReedsRepository.put`.
 */
export async function commitReedLikeLocally(cert: api.ReedLike): Promise<void> {
  await likedReedsRepository.put(cert);
}

/**
 * Effective liked state, overlaying any offline-queued action on top of the
 * confirmed record — a pending like/unlike is a real action the user took
 * and hasn't been undone, so it must count even before the server has
 * countersigned it. Pending state wins over the confirmed record since it's
 * always more recent (queuing an action clears the opposite pending entry).
 */
export async function isReedLiked(authorID: string, reedID: string): Promise<boolean> {
  if (await pendingLikeRepository.get(authorID, reedID)) return true;
  if (await pendingUnlikeRepository.get(authorID, reedID)) return false;
  return likedReedsRepository.has(authorID, reedID);
}

/** Put-then-side-effects. Returns false if verification fails. */
export async function verifyAndCommitReedLike(cert: api.ReedLike): Promise<boolean> {
  try {
    await commitReedLikeLocally(cert);
    return true;
  } catch (error) {
    console.error('[verifyAndCommitReedLike] refused', cert?.reedID, error);
    return false;
  }
}

/**
 * Signed path: queue pending → POST → verify countersig → likedReeds →
 * clear pending.
 */
export async function likeReed(authorID: string, reedID: string): Promise<api.ReedLike> {
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

  const userPayload = buildReedLikeUserPayload(serverID, refForReed(authorID, reedID), fingerprint);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  await pendingUnlikeRepository.delete(authorID, reedID);
  await pendingLikeRepository.put({
    compositeKey: `${authorID}:${reedID}`,
    authorID,
    reedID,
    serverID,
    fingerprint,
    signature,
  });

  const cert = await apiService.likeReed(refForReed(authorID, reedID), signature, fingerprint);
  if (!(await verifyAndCommitReedLike(cert))) {
    throw new Error('Server like countersignature failed verification');
  }
  await pendingLikeRepository.delete(authorID, reedID);
  return cert;
}

/**
 * Unsigned path: queue pending → DELETE → clear local like state →
 * clear pending. No signature is ever built or checked.
 */
export async function unlikeReed(authorID: string, reedID: string): Promise<void> {
  await pendingLikeRepository.delete(authorID, reedID);
  await pendingUnlikeRepository.put({
    compositeKey: `${authorID}:${reedID}`,
    authorID,
    reedID,
  });

  await apiService.unlikeReed(refForReed(authorID, reedID));
  await likedReedsRepository.delete(authorID, reedID);
  await pendingUnlikeRepository.delete(authorID, reedID);
}
