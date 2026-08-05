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
import { serverInfo, refreshServerInfo } from './serverInfo';
import {
  assertIdentityBackupKeys,
  type BackupPayload,
  writeIdentityKeysBackup,
} from './backupRestore';

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

  const { challenge } = await apiService.getAccountRecoveryChallenge();
  const sigArmor = await cryptoService.signMessage(
    String(challenge),
    privateKeyEntry.armor,
    passphrase
  );
  const signature = btoa(sigArmor);

  const bootstrap = await apiService.accountRecoveryBootstrap({
    challenge,
    userID: userId,
    fingerprint,
    signature,
  });

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
}
