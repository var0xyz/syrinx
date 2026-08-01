import { redirect } from '@sveltejs/kit';
import { getStorageQuota } from '$lib/services/pwa';
import { loadProfileKeyInfo } from './keyInfo';

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const [storage, keyInfo] = await Promise.all([
    getStorageQuota(),
    loadProfileKeyInfo(),
  ]);

  return { user, storage, keyInfo };
}
