import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { buildReedLikeUserPayload } from './signing';
import { pendingLikeRepository } from '$lib/repositories/pendingLike';
import { pendingUnlikeRepository } from '$lib/repositories/pendingUnlike';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { likedReedsRepository } from '$lib/repositories/likedReeds';
import { verifyReedLike } from '$lib/verifiers';
import { parseKeyId } from '$lib/utils/identityRef';

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
export async function isReedLiked(reedRef: string): Promise<boolean> {
  if (await pendingLikeRepository.get(reedRef)) return true;
  if (await pendingUnlikeRepository.get(reedRef)) return false;
  return likedReedsRepository.has(reedRef);
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
export async function likeReed(reedRef: string): Promise<api.ReedLike> {
  // serverID is stored on the pending record (a display/lookup field, not
  // part of the signed payload) — derived from reedRef, the reed's home
  // server, which is what the countersignature actually comes from.
  const serverID = parseKeyId(reedRef)?.serverId;
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

  const userPayload = buildReedLikeUserPayload(reedRef, fingerprint);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  await pendingUnlikeRepository.delete(reedRef);
  await pendingLikeRepository.put({
    compositeKey: reedRef,
    reedID: reedRef,
    serverID,
    fingerprint,
    signature,
  });

  const cert = await apiService.likeReed(reedRef, signature, fingerprint);
  if (!(await verifyAndCommitReedLike(cert))) {
    throw new Error('Server like countersignature failed verification');
  }
  await pendingLikeRepository.delete(reedRef);
  return cert;
}

/**
 * Unsigned path: queue pending → DELETE → clear local like state →
 * clear pending. No signature is ever built or checked.
 */
export async function unlikeReed(reedRef: string): Promise<void> {
  await pendingLikeRepository.delete(reedRef);
  await pendingUnlikeRepository.put({
    compositeKey: reedRef,
    reedID: reedRef,
  });

  await apiService.unlikeReed(reedRef);
  await likedReedsRepository.delete(reedRef);
  await pendingUnlikeRepository.delete(reedRef);
}
