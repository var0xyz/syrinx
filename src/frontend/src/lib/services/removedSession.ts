import { goto } from '$app/navigation';
import type * as api from '$lib/types/api';
import { verifyAccountRemoval } from '$lib/verifiers';
import { apiService } from './api';
import { authService } from './auth';
import { dbService } from './db';
import { clearWrappingKey } from './keyVault';
import { localStorageService } from './localstorage';

/** What /removed shows: who removed the account and why. */
export type RemovedState = { kicked: boolean; reason: string };

let signingOut = false;

/** The viewer's own removal: once it verifies against keys already held
 * (a removed device can't fetch any), wipe the device and say why. */
export async function signOutRemovedAccount(cert: api.AccountRemoval): Promise<void> {
  const viewerID = localStorage.getItem('userId');
  if (signingOut || !viewerID || cert.userId !== viewerID) return;
  signingOut = true;
  try {
    if (!(await verifyAccountRemoval(cert, { cachedKeysOnly: true }))) {
      console.warn('Own account removal failed verification; staying signed in');
      return;
    }
    const state: RemovedState = { kicked: !!cert.admin, reason: cert.admin?.request?.reason ?? cert.note ?? '' };
    localStorageService.clearAllData();
    await dbService.deleteDatabase();
    await clearWrappingKey();
    await authService.clearSession();
    await goto('/removed', { state, replaceState: true });
  } finally {
    signingOut = false;
  }
}

/** The socket closed because the account was removed: any signed request
 * is refused with the certificate, which the api reporter hands over. */
export async function probeOwnRemoval(): Promise<void> {
  const viewerID = localStorage.getItem('userId');
  if (!viewerID) return;
  await apiService.getUserInfo(viewerID).catch(() => {});
}
