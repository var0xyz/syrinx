/**
 * Moves this device from a server key it trusts to the key the server now
 * signs with, along the signed revocations between them. Only revocations
 * that aren't compromised are followed: a stolen key can sign a successor too.
 */

import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { verifyResponseEnvelope } from './responseVerifier';
import { getTrustedServerKey, setTrustedServerKey } from './serverKeyTrust';
import { buildServerKeyRevocationPayload } from './signing';
import { signedAtHeader } from './verify';
import { formatServerKeyId } from '$lib/utils/identityRef';

export type FollowResult =
  | { status: 'followed' }
  | { status: 'compromised'; reason: string }
  | { status: 'failed' };

/** Checks one revocation against the key it revokes. */
async function verifyRevocation(
  serverID: string,
  rev: api.ServerKeyRevocation,
  revokedArmor: string
): Promise<boolean> {
  const successorFingerprint = await cryptoService.fingerprintFromArmor(rev.successor.armor);
  if (formatServerKeyId(successorFingerprint, serverID) !== rev.successor.id) return false;

  const payload = buildServerKeyRevocationPayload(
    serverID,
    rev.keyID,
    rev.successor.id,
    rev.compromised,
    rev.reason,
    signedAtHeader(rev.signedAt)
  );
  return (
    (await cryptoService.verifySignature(payload, rev.signature, revokedArmor)) &&
    (await cryptoService.verifySignature(payload, rev.successorSignature, rev.successor.armor))
  );
}

/** Caches each revoked key with its state, so verifiers can apply the
 * compromised-key timestamp rule; the successor too only when adopted. */
async function cacheChainKeys(
  revs: api.ServerKeyRevocation[],
  firstArmor: string,
  adoptTip: boolean
): Promise<void> {
  const { allowUnsigned } = await import('$lib/verifiers');
  const armorOf = new Map<string, string>([[revs[0].keyID, firstArmor]]);
  for (const rev of revs) armorOf.set(rev.successor.id, rev.successor.armor);

  for (const rev of revs) {
    const key: api.PublicKey = {
      id: rev.keyID,
      userID: '',
      armor: armorOf.get(rev.keyID)!,
      revoked: true,
      predecessor: null,
      serverSignature: { id: '', armor: '', timestamp: '' },
      revokedAt: rev.signedAt,
      compromised: rev.compromised,
    };
    await dbService.put('publicKeys', key, allowUnsigned);
  }
  if (!adoptTip) return;
  const tip = revs[revs.length - 1].successor;
  await dbService.put(
    'publicKeys',
    {
      id: tip.id,
      userID: '',
      armor: tip.armor,
      revoked: false,
      predecessor: revs[revs.length - 1].keyID,
      serverSignature: { id: '', armor: '', timestamp: '' },
    },
    allowUnsigned
  );
}

/**
 * Follows the server key chain from the trusted key. Needs a signed-in user:
 * the chain is only served to registered users.
 */
export async function followServerKeyChain(): Promise<FollowResult> {
  const trusted = getTrustedServerKey();
  const serverID = localStorage.getItem('serverId');
  if (!trusted || !serverID || !authService.isLoggedIn() || !authService.getActiveKeyId()) {
    return { status: 'failed' };
  }

  try {
    const res = await apiService.getServerKeyChainUnverified(formatServerKeyId(trusted.fingerprint, serverID));
    const body = (await res.clone().json()) as { revocations?: api.ServerKeyRevocation[] };
    const revs = body.revocations ?? [];
    if (revs.length === 0) return { status: 'failed' };

    let currentID = formatServerKeyId(trusted.fingerprint, serverID);
    let currentArmor = trusted.armor;
    for (const [i, rev] of revs.entries()) {
      if (rev.keyID !== currentID || !(await verifyRevocation(serverID, rev, currentArmor))) {
        console.error('[serverKeyChain] revocation failed verification', rev.keyID);
        return { status: 'failed' };
      }
      if (rev.compromised) {
        await cacheChainKeys(revs.slice(0, i + 1), trusted.armor, false);
        return { status: 'compromised', reason: rev.reason };
      }
      currentID = rev.successor.id;
      currentArmor = rev.successor.armor;
    }

    if (!(await verifyResponseEnvelope(res, currentArmor))) {
      console.error('[serverKeyChain] response not signed by the chain\'s last key');
      return { status: 'failed' };
    }

    await cacheChainKeys(revs, trusted.armor, true);
    await setTrustedServerKey(currentArmor);
    return { status: 'followed' };
  } catch (error) {
    console.error('[serverKeyChain] could not follow the server key chain', error);
    return { status: 'failed' };
  }
}
