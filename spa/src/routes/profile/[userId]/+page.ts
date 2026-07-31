import { redirect } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';

/** @type {import('./$types').PageLoad} */
export async function load({ params, parent }) {
  const { user: currentUser } = await parent();
  if (!currentUser) {
    throw redirect(307, '/');
  }

  const userId = params.userId;
  const isOwner = currentUser.id === userId;

  if (await userRepository.isTombstone(userId)) {
    const cert = await removedAccountsRepository.get(userId);
    return {
      currentUser,
      userId,
      isOwner,
      status: 'tombstone',
      profileUser: null,
      tombstoneNote: cert?.note ?? '',
      fromCache: true,
    };
  }

  const localReeds = await reedsService.getReedsByAuthor(userId);
  if (localReeds.length > 0) {
    const profileUser = isOwner
      ? currentUser
      : await userRepository.get(userId).catch(() => null);
    return {
      currentUser,
      userId,
      isOwner,
      status: 'ready',
      profileUser,
      tombstoneNote: '',
      fromCache: true,
    };
  }

  return {
    currentUser,
    userId,
    isOwner,
    status: 'loading',
    profileUser: null,
    tombstoneNote: '',
    fromCache: false,
  };
}
