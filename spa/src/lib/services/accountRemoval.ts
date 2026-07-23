import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import {
  buildAccountRemovalServerPayload,
  buildAccountRemovalUserPayload,
} from './signing';
import { signedAtHeader, verify } from './verify';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { dbService } from './db';
import { get } from 'svelte/store';
import { isOnline } from './pwa';
import { serverInfo } from './serverInfo';

const MAX_NOTE_LEN = 140;

/** Verify author + server signatures on an account-removal cert. */
export async function verifyAccountRemoval(cert: api.AccountRemoval): Promise<boolean> {
  if (!cert || cert.type !== 'account' || !cert.signature || !cert.server) {
    console.error('[verifyAccountRemoval] missing fields or wrong type', cert?.type);
    return false;
  }
  if ((cert.note?.length ?? 0) > MAX_NOTE_LEN) {
    console.error('[verifyAccountRemoval] note too long');
    return false;
  }

  const userPayload = buildAccountRemovalUserPayload(
    cert.serverID,
    cert.userID,
    cert.note ?? ''
  );
  const armor = await resolveAuthorArmor(cert.userID);
  if (!armor) {
    console.error('[verifyAccountRemoval] no public key for author', cert.userID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(cert.signature);
  } catch {
    console.error('[verifyAccountRemoval] invalid signature encoding');
    return false;
  }

  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyAccountRemoval] user signature failed', cert.userID);
    return false;
  }

  const serverPayload = buildAccountRemovalServerPayload(
    cert.serverID,
    cert.userID,
    cert.note ?? '',
    cert.server.fingerprint,
    cert.signature,
    signedAtHeader(cert.server.timestamp)
  );
  const serverResult = await verify(cert.server, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyAccountRemoval] server signature failed', serverResult);
    return false;
  }
  return true;
}

async function resolveAuthorArmor(userID: string): Promise<string | null> {
  try {
    // Prefer a cached key — profile GET may already be Gone.
    const allKeys = await dbService.getAll<api.PublicKey>('publicKeys');
    const forUser = allKeys.filter((k) => k.userID === userID && k.armor);
    if (forUser.length === 1) {
      return forUser[0].armor;
    }

    const user = await apiService.getUser(userID).catch(() => null);
    const fp =
      user?.activeKeyFingerprint ||
      user?.signatureFingerprint ||
      forUser[0]?.fingerprint;
    if (!fp) {
      return forUser[0]?.armor ?? null;
    }

    const cached = await publicKeyRepository.getPublicKey(fp);
    if (cached?.armor) return cached.armor;

    const key = await apiService.getPublicKey(userID, fp);
    if (key?.armor) {
      await publicKeyRepository.put(key);
      return key.armor;
    }
  } catch (error) {
    console.error('[verifyAccountRemoval] failed to resolve author key', userID, error);
  }
  return null;
}

/**
 * Peer purge set (07): drop profile/reeds/follows; keep public keys;
 * store cert for tombstone note.
 */
export async function commitAccountRemovalLocally(cert: api.AccountRemoval): Promise<void> {
  await removedAccountsRepository.put(cert);
  await reedsService.deleteReedsByAuthor(cert.userID);
  await dbService.delete('following', cert.userID);
  await dbService.delete('pendingFollows', cert.userID);
  await dbService.delete('unfollow', cert.userID);
  await userRepository.writeTombstone(cert.userID);
}

export async function verifyAndCommitAccountRemoval(
  cert: api.AccountRemoval
): Promise<boolean> {
  if (!(await verifyAccountRemoval(cert))) {
    return false;
  }
  await commitAccountRemovalLocally(cert);
  return true;
}

/**
 * Author path (online-only): sign → DELETE → verify countersig.
 * No offline pending queue — account deletion must succeed now or fail.
 * Caller is responsible for wiping the local device session afterward.
 */
export async function removeAccountAsAuthor(note: string = ''): Promise<api.AccountRemoval> {
  if (note.length > MAX_NOTE_LEN) {
    throw new Error(`Note must be at most ${MAX_NOTE_LEN} characters`);
  }
  if (!get(isOnline) || !navigator.onLine) {
    throw new Error('You must be online to delete your account');
  }

  const user = await authService.getCurrentUser();
  if (!user?.id) {
    throw new Error('Not logged in');
  }

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

  const userPayload = buildAccountRemovalUserPayload(serverID, user.id, note);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  const cert = await apiService.deleteAccount(signature, note);
  if (!(await verifyAccountRemoval(cert))) {
    throw new Error('Server account-removal countersignature failed verification');
  }
  return cert;
}
