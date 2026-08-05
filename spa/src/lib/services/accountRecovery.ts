/**
 * Account recovery: import keys-only `.sxi.gpg` and install session from
 * server bootstrap (profile, following, tip id).
 */

import type * as api from '$lib/types/api';
import { get } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { requestSigner } from './request-signer';
import { followingRepository } from '$lib/repositories/following';
import { reedRequestsRepository } from '$lib/repositories/reedRequests';
import { reedsService } from '$lib/repositories/reeds';
import { serverInfo, refreshServerInfo } from './serverInfo';
import { serverConnection } from './serverConnection';
import { startReedRequestDrainer, clearReedRequestDispatched } from './reedRequestDrainer';
import {
  assertIdentityBackupKeys,
  type BackupPayload,
  writeIdentityKeysBackup,
} from './backupRestore';

export function mapAccountRecoveryBootstrapError(err: unknown): Error {
  const status = (err as { status?: number })?.status;
  if (status === 404) {
    return new Error(
      'This account is not recognized on this server. You need a full backup to restore. ' +
        'If your community is rebuilding, import a backup while the server is in recovery mode.'
    );
  }
  if (status === 401) {
    return new Error(
      'Cannot recover with this key. Rotate from a device that still works, ' +
        'or restore a backup taken while the key was active.'
    );
  }
  if (err instanceof Error) {
    return err;
  }
  return new Error('Account recovery failed. Please try again.');
}

function assertServerMatch(backup: BackupPayload): void {
  const backupServerId = backup.localStorage?.['serverId'];
  if (!backupServerId) {
    throw new Error('Invalid identity backup: missing serverId.');
  }
  const current = get(serverInfo)?.id || localStorage.getItem('serverId');
  if (current && backupServerId !== current) {
    throw new Error('This identity backup belongs to a different server.');
  }
}

async function fetchBootstrap(
  userId: string,
  fingerprint: string,
  privateKeyArmor: string,
  passphrase: string
): Promise<api.AccountRecoveryBootstrapResponse> {
  const { challenge } = await apiService.getAccountRecoveryChallenge();
  const sigArmor = await cryptoService.signMessage(
    String(challenge),
    privateKeyArmor,
    passphrase
  );
  const signature = btoa(sigArmor);

  try {
    return await apiService.bootstrapAccountRecovery({
      challenge,
      userID: userId,
      fingerprint,
      signature,
    });
  } catch (err) {
    throw mapAccountRecoveryBootstrapError(err);
  }
}

/**
 * Decrypted keys-only backup → challenge/bootstrap → session with server profile.
 * Does not write a full backup snapshot.
 */
export async function restoreFromIdentityBackup(backup: BackupPayload): Promise<void> {
  assertIdentityBackupKeys(backup);
  await refreshServerInfo();
  assertServerMatch(backup);

  const ls = backup.localStorage ?? {};
  const userId = ls['userId']!;
  const fingerprint = ls['keyFingerprint']!;
  const passphrase = ls['keyPassphrase']!;

  const privateKeysTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'privateKeys');
  const privateKeyEntry = (privateKeysTable?.items ?? []).find(
    (k) => k && typeof k === 'object' && (k as { fingerprint?: string }).fingerprint === fingerprint
  ) as { armor?: string } | undefined;

  if (!privateKeyEntry?.armor) {
    throw new Error('Invalid identity backup: missing private key armor.');
  }

  const bootstrap = await fetchBootstrap(userId, fingerprint, privateKeyEntry.armor, passphrase);

  await writeIdentityKeysBackup(backup);

  await authService.saveUserToStorage(bootstrap.profile);
  authService.setActiveKey(fingerprint);

  for (const followId of bootstrap.following) {
    await followingRepository.recordLocalFollow(followId);
  }

  if (bootstrap.tipReedID) {
    localStorage.setItem('publishTipReedID', bootstrap.tipReedID);
  } else {
    localStorage.removeItem('publishTipReedID');
  }

  await requestSigner.initializeWorker(fingerprint, passphrase);
  await apiService.bindDevice();

  const serverId = get(serverInfo)?.id ?? localStorage.getItem('serverId') ?? '';
  if (serverId) {
    const skip = new Set<string>();
    for (const reedId of bootstrap.reedIDs) {
      if (await reedsService.getReed(userId, reedId)) {
        skip.add(reedId);
      }
    }
    await reedRequestsRepository.seedReedIDs(serverId, userId, bootstrap.reedIDs, skip);
  }

  await serverConnection.connect();
  clearReedRequestDispatched();
  serverConnection.syncRequest();
  startReedRequestDrainer();
}
