/**
 * Moves this device from a server key it trusts to the key the server now
 * signs with, one signed revocation at a time. Only revocations that aren't
 * compromised are trusted: a stolen key can sign a successor too.
 */

import type * as api from '$lib/types/api';
import { apiService, readKeyRevocation, readPublicKey } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { verifyResponseEnvelope } from './responseVerifier';
import { getTrustedServerKey, setTrustedServerKey } from './serverKeyTrust';
import { buildServerKeyRevocationPayload } from './signing';
import { signedAtHeader } from './verify';
import { formatServerKeyId } from '$lib/utils/identityRef';

/** More revocations than this between two visits falls back to the key gate. */
const MAX_HOPS = 10;

export type KeyUpdateResult =
  | { status: 'updated' }
  | { status: 'compromised'; reason: string }
  | { status: 'failed' };

type Hop = { rev: api.ServerKeyRevocation; revokedArmor: string; successorArmor: string };

/** Checks a revocation against the key it revokes and its successor. */
async function verifyRevocation(serverID: string, hop: Hop): Promise<boolean> {
  const { rev, revokedArmor, successorArmor } = hop;
  const successorFingerprint = await cryptoService.fingerprintFromArmor(successorArmor);
  if (formatServerKeyId(successorFingerprint, serverID) !== rev.successor) return false;

  const payload = buildServerKeyRevocationPayload(
    serverID,
    rev.keyID,
    rev.successor,
    rev.compromised,
    rev.reason,
    signedAtHeader(rev.signedAt)
  );
  return (
    (await cryptoService.verifySignature(payload, rev.signature, revokedArmor)) &&
    (await cryptoService.verifySignature(payload, rev.successorSignature, successorArmor))
  );
}

/** Caches each revoked key with its state, so verifiers can apply the
 * compromised-key timestamp rule; the newest successor only when adopted. */
async function cacheKeys(hops: Hop[], adoptNewest: boolean): Promise<void> {
  const { allowUnsigned } = await import('$lib/verifiers');
  const unsigned = { id: '', armor: '', timestamp: '' };
  for (const { rev, revokedArmor } of hops) {
    const key: api.PublicKey = {
      id: rev.keyID,
      userID: '',
      armor: revokedArmor,
      revoked: true,
      predecessor: null,
      serverSignature: unsigned,
      revokedAt: rev.signedAt,
      compromised: rev.compromised,
    };
    await dbService.put('publicKeys', key, allowUnsigned);
  }
  if (!adoptNewest) return;
  const last = hops[hops.length - 1];
  const newest: api.PublicKey = {
    id: last.rev.successor,
    userID: '',
    armor: last.successorArmor,
    revoked: false,
    predecessor: last.rev.keyID,
    serverSignature: unsigned,
  };
  await dbService.put('publicKeys', newest, allowUnsigned);
}

/** The revocation of keyID, or null when the server says it isn't revoked. */
async function fetchRevocation(keyID: string): Promise<{ res: Response; rev: api.ServerKeyRevocation } | null> {
  try {
    const res = await apiService.getKeyRevocationUnverified(keyID);
    return { res, rev: (await readKeyRevocation(res)) as api.ServerKeyRevocation };
  } catch (error) {
    if ((error as { status?: number })?.status === 404) return null;
    throw error;
  }
}

/**
 * Follows the server's key revocations from the trusted key. Needs a
 * signed-in user: the revocation and key endpoints take signed requests.
 */
export async function updateTrustedServerKey(): Promise<KeyUpdateResult> {
  const trusted = getTrustedServerKey();
  const serverID = localStorage.getItem('serverId');
  if (!trusted || !serverID || !authService.isLoggedIn() || !authService.getActiveKeyId()) {
    return { status: 'failed' };
  }

  try {
    const hops: Hop[] = [];
    // Signed by the current key, which is only known once the last hop verifies.
    const responses: Response[] = [];
    let currentID = formatServerKeyId(trusted.fingerprint, serverID);
    let currentArmor = trusted.armor;

    for (let i = 0; i < MAX_HOPS; i++) {
      const fetched = await fetchRevocation(currentID);
      if (!fetched) break;
      const { res, rev } = fetched;
      if (rev.type !== 'server-key-revocation' || rev.serverID !== serverID || rev.keyID !== currentID) {
        return { status: 'failed' };
      }
      const keyRes = await apiService.getPublicKeyUnverified(rev.successor);
      const successor = await readPublicKey(keyRes);
      const hop = { rev, revokedArmor: currentArmor, successorArmor: successor.armor };
      if (!(await verifyRevocation(serverID, hop))) {
        console.error('[serverKeyRotation] revocation failed verification', rev.keyID);
        return { status: 'failed' };
      }
      hops.push(hop);
      if (rev.compromised) {
        await cacheKeys(hops, false);
        return { status: 'compromised', reason: rev.reason };
      }
      responses.push(res, keyRes);
      currentID = rev.successor;
      currentArmor = successor.armor;
    }

    if (hops.length === 0) return { status: 'failed' };
    for (const res of responses) {
      if (!(await verifyResponseEnvelope(res, currentArmor))) {
        console.error('[serverKeyRotation] response not signed by the new key');
        return { status: 'failed' };
      }
    }

    await cacheKeys(hops, true);
    await setTrustedServerKey(currentArmor);
    return { status: 'updated' };
  } catch (error) {
    console.error('[serverKeyRotation] could not update the trusted server key', error);
    return { status: 'failed' };
  }
}
