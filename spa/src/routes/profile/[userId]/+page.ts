import { redirect } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { userInfoRepository } from '$lib/repositories/userInfo';
import { followingRepository } from '$lib/repositories/following';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { mergeUserView } from '$lib/utils/userView';

/** @type {import('./$types').PageLoad} */
export async function load({ params, parent }) {
  const { user: currentUser } = await parent();
  if (!currentUser) {
    throw redirect(307, '/');
  }

  const userId = params.userId;
  const isOwner = currentUser.id === userId;
  const isFollowing = !isOwner && (await followingRepository.isFollowing(userId));

  const removedCert = await removedAccountsRepository.get(userId);
  if (removedCert) {
    return {
      currentUser,
      userId,
      isOwner,
      isFollowing,
      status: 'tombstone',
      profileUser: null,
      tombstoneNote: removedCert.note ?? '',
      fromCache: true,
      accountRemoved: true,
    };
  }

  if (await userRepository.isTombstone(userId)) {
    return {
      currentUser,
      userId,
      isOwner,
      isFollowing,
      status: 'tombstone',
      profileUser: null,
      tombstoneNote: '',
      fromCache: true,
      accountRemoved: true,
    };
  }

  const cachedProfile = isOwner
    ? currentUser
    : await userRepository.get(userId).catch(() => null);
  const cachedInfo = await userInfoRepository.get(userId).catch(() => null);
  const profileUser = mergeUserView(cachedProfile, cachedInfo);

  const localReeds = await reedsService.getReedsByAuthor(userId);
  if (localReeds.length > 0 || profileUser) {
    return {
      currentUser,
      userId,
      isOwner,
      isFollowing,
      status: profileUser ? 'ready' : 'loading',
      profileUser,
      tombstoneNote: '',
      fromCache: true,
      accountRemoved: false,
    };
  }

  return {
    currentUser,
    userId,
    isOwner,
    isFollowing,
    status: 'loading',
    profileUser: null,
    tombstoneNote: '',
    fromCache: false,
    accountRemoved: false,
  };
}
