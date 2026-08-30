import { redirect } from '@sveltejs/kit';
import { getStorageQuota } from '$lib/services/pwa';
import { userInfoRepository } from '$lib/repositories/userInfo';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { mergeUserView } from '$lib/utils/userView';
import { loadProfileKeyInfo } from './keyInfo';

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const [storage, keyInfo, cachedInfo] = await Promise.all([
    getStorageQuota(),
    loadProfileKeyInfo(),
    userInfoRepository.get(user.id).catch(() => null),
  ]);

  const activeKeyMintedAt = await privateKeyRepository.getMintedAt(keyInfo.keyId);

  // Read ahead of render so the "last backup" / stale-backup line has its
  // answer the moment the page paints, instead of the onMount-driven flash
  // that showed on every navigation here before.
  const storedBackupAt = localStorage.getItem('lastBackupAt');
  const storedKeyBackupAt = localStorage.getItem('lastKeyBackupAt');

  return {
    user: mergeUserView(user, cachedInfo) ?? user,
    storage,
    keyInfo,
    lastBackupAt: storedBackupAt ? parseInt(storedBackupAt) : null,
    lastKeyBackupAt: storedKeyBackupAt ? parseInt(storedKeyBackupAt) : null,
    activeKeyMintedAt,
  };
}
