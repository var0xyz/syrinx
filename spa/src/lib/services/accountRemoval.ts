import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { buildAccountRemovalUserPayload } from './signing';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { dbService } from './db';
import { verifyAccountRemoval } from '$lib/verifiers';
import { get } from 'svelte/store';
import { isOnline } from './pwa';
import { serverInfo } from './serverInfo';

const MAX_NOTE_LEN = 140;

export { verifyAccountRemoval };

/**
 * Peer purge set (07): drop profile/reeds/follows; keep public keys;
 * store cert for tombstone note. Verification runs inside
 * `removedAccountsRepository.put`.
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
  try {
    await commitAccountRemovalLocally(cert);
    return true;
  } catch (error) {
    console.error('[verifyAndCommitAccountRemoval] refused', cert?.userID, error);
    return false;
  }
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
