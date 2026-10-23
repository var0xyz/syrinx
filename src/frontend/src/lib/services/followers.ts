import type * as api from '$lib/types/api';
import { followersRepository } from '$lib/repositories/followers';
import { verifyFollowCert } from '$lib/verifiers';
import { serverConnection } from './serverConnection';

/** Handles a pushed FOLLOWED: keeps a follow of the viewer, drops it on an
 * unfollow, and acks. An older certificate than the one held changes nothing. */
export async function receiveFollow(cert: api.FollowCert): Promise<void> {
  const viewerID = localStorage.getItem('userId');
  if (!viewerID || cert.followeeId !== viewerID) return;
  const held = await followersRepository.get(cert.followerId);
  const newer = !held || (cert.serverSignature?.signedAt ?? 0) > (held.serverSignature?.signedAt ?? 0);
  try {
    if (newer && cert.follow) {
      await followersRepository.put(cert);
    } else if (newer) {
      if (!(await verifyFollowCert(cert))) throw new Error('unfollow failed verification');
      await followersRepository.delete(cert.followerId);
    }
  } catch (error) {
    console.warn('[followers] refused follow certificate', cert.followerId, error);
    return;
  }
  serverConnection.sendFollowedAck(cert.followerId, cert.follow);
}
