import { redirect } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { parseReedRef, canonicalReedId } from '$lib/utils/reedRef';

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
      canonicalReedID: null,
      reed: null,
      authorUser: null,
      echoedReed: null,
      echoedReedMissing: false,
      repliedToReed: null,
      repliedToReedMissing: false,
      errorMessage: 'This reed does not belong to the specified user',
      fromCache: false,
    };
  }

  let authorUser = null;
  let echoedReed = null;
  let echoedReedMissing = false;
  let repliedToReed = null;
  let repliedToReedMissing = false;
  let removedReedCert = null;
  let removedAccountCert = null;

  if (!reed) {
    removedAccountCert = await removedAccountsRepository.get(userID);
    if (!removedAccountCert) {
      removedReedCert = await removedReedsRepository.get(reedID);
    }
    if (removedReedCert || removedAccountCert) {
      authorUser = await userRepository.get(userID).catch(() => null);
    }
  }

  if (reed) {
    authorUser = await userRepository.get(userID).catch(() => null);

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
    // Canonical id (authorID@serverID/uuid) — the single value every
    // reed-scoped API call/subscription below the route boundary should
    // use instead of the separate userID/reedID URL segments above.
    canonicalReedID: reed ? canonicalReedId(reed) : null,
    reed,
    authorUser,
    echoedReed,
    echoedReedMissing,
    repliedToReed,
    repliedToReedMissing,
    removedReedCert,
    removedAccountCert,
    errorMessage: '',
    fromCache: !!(reed || removedReedCert || removedAccountCert),
  };
}
