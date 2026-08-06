import { redirect } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { echoCountsRepository } from '$lib/repositories/echoCounts';
import { replyCountsRepository } from '$lib/repositories/replyCounts';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { parseReedRef } from '$lib/utils/reedRef';

/** @type {import('./$types').PageLoad} */
export async function load({ params, parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const userID = params.userID;
  const reedID = params.reedID;

  let reed = await reedsService.getReed(userID, reedID);
  if (!reed && user.id === userID) {
    const pending = await reedsService.getUnsignedReed(reedID);
    if (pending?.userID === userID) {
      reed = pending;
    }
  }

  if (reed && reed.userID !== userID) {
    return {
      user,
      userID,
      reedID,
      reed: null,
      authorUser: null,
      echoedReed: null,
      echoedReedMissing: false,
      repliedToReed: null,
      repliedToReedMissing: false,
      echoCount: 0,
      replyCount: 0,
      errorMessage: 'This reed does not belong to the specified user',
      fromCache: false,
    };
  }

  let authorUser = null;
  let echoedReed = null;
  let echoedReedMissing = false;
  let repliedToReed = null;
  let repliedToReedMissing = false;
  let echoCount = 0;
  let replyCount = 0;
  let removedReedCert = null;
  let removedAccountCert = null;

  if (!reed) {
    removedAccountCert = await removedAccountsRepository.get(userID);
    if (!removedAccountCert) {
      removedReedCert = await removedReedsRepository.get(reedID);
    }
    if (removedReedCert || removedAccountCert) {
      authorUser = await userRepository.get(userID).catch(() => null);
      echoCount = await echoCountsRepository.get(reedID);
      replyCount = await replyCountsRepository.get(reedID);
    }
  }

  if (reed) {
    authorUser = await userRepository.get(userID).catch(() => null);
    echoCount = await echoCountsRepository.get(reedID);
    replyCount = await replyCountsRepository.get(reedID);

    if (reed.echoing) {
      const echoRef = parseReedRef(reed.echoing);
      if (echoRef) {
        echoedReed = await reedsService.getReed(echoRef.authorId, echoRef.reedId);
        echoedReedMissing = false;
      } else {
        echoedReedMissing = true;
      }
    }

    if (reed.replying) {
      const replyRef = parseReedRef(reed.replying);
      if (replyRef) {
        repliedToReed = await reedsService.getReed(replyRef.authorId, replyRef.reedId);
        repliedToReedMissing = false;
      } else {
        repliedToReedMissing = true;
      }
    }
  }

  return {
    user,
    userID,
    reedID,
    reed,
    authorUser,
    echoedReed,
    echoedReedMissing,
    repliedToReed,
    repliedToReedMissing,
    echoCount,
    replyCount,
    removedReedCert,
    removedAccountCert,
    errorMessage: '',
    fromCache: !!(reed || removedReedCert || removedAccountCert),
  };
}
