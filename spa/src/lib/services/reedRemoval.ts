import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import {
  buildReedRemovalServerPayload,
  buildReedRemovalUserPayload,
} from './signing';
import { signedAtHeader, verify } from './verify';
import { pendingRemovalRepository } from '$lib/repositories/pendingRemoval';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { get } from 'svelte/store';
import { serverInfo } from './serverInfo';

/** Verify author + server signatures on a reed-removal cert. */
export async function verifyReedRemoval(cert: api.ReedRemoval): Promise<boolean> {
  if (
    !cert ||
    cert.type !== 'reed' ||
    !cert.userSignature?.armor ||
    !cert.serverSignature
  ) {
    console.error('[verifyReedRemoval] missing fields or wrong type', cert?.type);
    return false;
  }

  const userPayload = buildReedRemovalUserPayload(cert.serverID, cert.userID, cert.reedID);
  const armor = await resolveAuthorArmor(cert.userID);
  if (!armor) {
    console.error('[verifyReedRemoval] no public key for author', cert.userID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(cert.userSignature.armor);
  } catch {
    console.error('[verifyReedRemoval] invalid signature encoding');
    return false;
  }

  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyReedRemoval] user signature failed', cert.reedID);
    return false;
  }

  const serverPayload = buildReedRemovalServerPayload(
    cert.serverID,
    cert.userID,
    cert.reedID,
    cert.serverSignature.fingerprint,
    cert.userSignature.armor,
    signedAtHeader(cert.serverSignature.timestamp)
  );
  const serverResult = await verify(cert.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyReedRemoval] server signature failed', serverResult);
    return false;
  }
  return true;
}

async function resolveAuthorArmor(userID: string): Promise<string | null> {
  try {
    const user = await apiService.getUser(userID);
    const fp = user.activeKeyFingerprint || user.userSignature?.fingerprint;
    if (!fp) return null;

    const cached = await publicKeyRepository.getPublicKey(fp);
    if (cached?.armor) return cached.armor;

    const key = await apiService.getPublicKey(userID, fp);
    if (key?.armor) {
      await publicKeyRepository.put(key);
      return key.armor;
    }
  } catch (error) {
    console.error('[verifyReedRemoval] failed to resolve author key', userID, error);
  }
  return null;
}

/**
 * Persist cert then drop the local reed. Does not clear pendingRemoval —
 * callers that own the pending row clear it after this succeeds.
 */
export async function commitReedRemovalLocally(cert: api.ReedRemoval): Promise<void> {
  await removedReedsRepository.put(cert);
  await dbService.delete('reeds', cert.reedID);
}

/** Verify then commit. Returns false if verification fails (reed retained). */
export async function verifyAndCommitReedRemoval(cert: api.ReedRemoval): Promise<boolean> {
  if (!(await verifyReedRemoval(cert))) {
    return false;
  }
  await commitReedRemovalLocally(cert);
  return true;
}

/**
 * Author path: queue pending → DELETE → verify countersig →
 * removedReeds → delete reed → clear pending.
 */
export async function removeReedAsAuthor(userID: string, reedID: string): Promise<api.ReedRemoval> {
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

  const userPayload = buildReedRemovalUserPayload(serverID, userID, reedID);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  await pendingRemovalRepository.put({
    reedID,
    serverID,
    userID,
    signature,
  });

  const cert = await apiService.deleteReed(userID, reedID, signature);
  if (!(await verifyAndCommitReedRemoval(cert))) {
    throw new Error('Server reed-removal countersignature failed verification');
  }
  await pendingRemovalRepository.delete(reedID);
  return cert;
}
