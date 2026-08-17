import { error, redirect } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { isValidRef, getUserId } from '$lib/utils/identityRef';

/** @type {import('./$types').PageLoad} */
export async function load({ params, parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  // The URL's one param IS the canonical reed ref (authorID@serverID/uuid)
  // — never split across segments, never reassembled from parts.
  const canonicalReedID = params.reedID;
  if (!isValidRef(canonicalReedID)) {
    throw error(404, 'Not found');
  }
  const userID = getUserId(canonicalReedID);

  let reed = await reedsService.getReed(canonicalReedID);
  if (!reed && user.id === userID) {
    const pending = await reedsService.getUnsignedReed(canonicalReedID);
    if (pending?.userID === userID) {
      reed = pending;
    }
  }

  if (reed && reed.userID !== userID) {
    return {
      user,
      userID,
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
      removedReedCert = await removedReedsRepository.get(canonicalReedID);
    }
    if (removedReedCert || removedAccountCert) {
      authorUser = await userRepository.get(userID).catch(() => null);
    }
  }

  if (reed) {
    authorUser = await userRepository.get(userID).catch(() => null);

    if (reed.echoing) {
      if (isValidRef(reed.echoing)) {
        echoedReed = await reedsService.getReed(reed.echoing);
        echoedReedMissing = false;
      } else {
        echoedReedMissing = true;
      }
    }

    if (reed.replying) {
      if (isValidRef(reed.replying)) {
        repliedToReed = await reedsService.getReed(reed.replying);
        repliedToReedMissing = false;
      } else {
        repliedToReedMissing = true;
      }
    }
  }

  return {
    user,
    userID,
    // Canonical id (authorID@serverID/uuid) — the single value every
    // reed-scoped API call/subscription below the route boundary should use.
    canonicalReedID,
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
