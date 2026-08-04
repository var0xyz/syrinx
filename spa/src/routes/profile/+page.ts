import { redirect } from '@sveltejs/kit';
import { getStorageQuota } from '$lib/services/pwa';
import { userInfoRepository } from '$lib/repositories/userInfo';
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

  return {
    user: mergeUserView(user, cachedInfo) ?? user,
    storage,
    keyInfo,
  };
}
