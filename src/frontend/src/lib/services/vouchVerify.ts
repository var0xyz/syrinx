import { apiService } from './api';
import { cryptoService } from './crypto';
import { verifyPublicKey } from '$lib/verifiers';
import { parseKeyId, formatKeyId, appendFingerprint } from '$lib/utils/identityRef';
import { vouchesRepository } from '$lib/repositories/vouches';
import { foreignServerOf, unreachableServerMessage } from './peerServers';
import type * as api from '$lib/types/api';
import { fromUnix } from '$lib/utils/time';

/**
 * Outcome of comparing a scanned key id against the key we resolved. There
 * is no "first contact" case: the scanner always fetches and derives, so
 * there is always something to compare. 'unresolvable' is not a comparison
 * result but a blocked flow.
 */
export type CompareOutcome =
  | 'same'
  | 'differs'
  | 'unresolvable'
  | 'already-verified'
  | 'server-unreachable';

export interface CompareResult {
  outcome: CompareOutcome;
  /** The id the subject showed, which is what a vouch would attest. */
  scannedKeyID: string;
  /** The id we derived from the armor the server served, if any. */
  servedKeyID: string | null;
  /** Set when the flow must not continue. */
  reason?: string;
  /** When the caller already verified this key, for 'already-verified'. */
  verifiedAt?: string;
}

/** The link the subject shows. Only the fingerprint rides in the fragment,
 * which is never sent to the server, so it reaches the scanner untouched. */
export function vouchLinkFor(userID: string, fingerprint: string, origin: string): string {
  return `${origin}/profile/${userID}/vouch#${fingerprint}`;
}

/** An OpenPGP v4 or v6 fingerprint, as hex. */
const FINGERPRINT_PATTERN = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

/**
 * Builds the key id a /profile/{userID}/vouch#fingerprint link names. The
 * scanner composes it from the profile it is on, so the fragment can only
 * name a key of that account, never a full id to take on trust.
 */
export function parseVouchFragment(fragment: string, subjectUserID: string): string | null {
  const fingerprint = fragment.replace(/^#/, '').trim().toLowerCase();
  if (!FINGERPRINT_PATTERN.test(fingerprint)) return null;
  return appendFingerprint(subjectUserID, fingerprint);
}

/**
 * Resolves the subject's key and derives its id from the armor, rather than
 * trusting the id the response labels it with. Without this a server could
 * serve a key it controls under the id the QR names, and the comparison
 * below would match on a counterfeit.
 */
async function resolveServedKeyID(
  subjectUserID: string
): Promise<{ keyID: string | null; unreachable: boolean }> {
  let key: api.PublicKey | null = null;
  try {
    const info = await apiService.getUserInfo(subjectUserID);
    if (!info?.activeKeyId) return { keyID: null, unreachable: false };
    key = await apiService.getPublicKey(info.activeKeyId);
  } catch (error) {
    console.error('[vouchVerify] could not resolve served key', subjectUserID, error);
    return { keyID: null, unreachable: true };
  }
  if (!key?.armor) return { keyID: null, unreachable: false };

  // verifyPublicKey re-derives the fingerprint from the armor and rejects a
  // mismatch, so a key that passes here really is the key it claims to be.
  if (!(await verifyPublicKey(key))) {
    console.error('[vouchVerify] served key failed verification', key.id);
    return { keyID: null, unreachable: false };
  }
  const derived = await cryptoService.fingerprintFromArmor(key.armor);
  const parsed = parseKeyId(key.id);
  if (!parsed) return { keyID: null, unreachable: false };
  return { keyID: formatKeyId(parsed.userId, parsed.serverId, derived), unreachable: false };
}

/** A live vouch the caller already made for this exact key, if any. */
async function existingVouchFor(
  subjectUserID: string,
  scannedKeyID: string
): Promise<api.Vouch | null> {
  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  if (!me) return null;
  const held = await vouchesRepository.forSubject(subjectUserID);
  return (
    held.find(
      (v) => !v.withdrawal && v.voucherUserId === me && v.subjectKeyId === scannedKeyID
    ) ?? null
  );
}

/**
 * Compares the scanned key id against the subject's current key. The
 * scanned id is the out-of-band evidence; anything the server says is the
 * thing being checked, never the baseline.
 */
export async function compareScannedKey(
  subjectUserID: string,
  scannedKeyID: string
): Promise<CompareResult> {
  const parsed = parseKeyId(scannedKeyID);
  if (!parsed) {
    return {
      outcome: 'unresolvable',
      scannedKeyID,
      servedKeyID: null,
      reason: 'The scanned code is not a valid key id.',
    };
  }

  // Verifying a key you already vouched for asserts nothing new, and your
  // existing signature stands even if you have since rotated your own key.
  const held = await existingVouchFor(subjectUserID, scannedKeyID);
  if (held) {
    return {
      outcome: 'already-verified',
      scannedKeyID,
      servedKeyID: scannedKeyID,
      verifiedAt: fromUnix(held.serverSignature?.signedAt ?? 0)?.toISOString(),
    };
  }

  const served = await resolveServedKeyID(subjectUserID);
  const servedKeyID = served.keyID;

  // Another server's user: their key comes from their server, right now.
  if (!servedKeyID && served.unreachable && foreignServerOf(subjectUserID)) {
    return {
      outcome: 'server-unreachable',
      scannedKeyID,
      servedKeyID: null,
      reason: await unreachableServerMessage(subjectUserID),
    };
  }
  if (!servedKeyID) {
    return {
      outcome: 'unresolvable',
      scannedKeyID,
      servedKeyID: null,
      reason: 'Could not confirm this account’s current key.',
    };
  }
  if (servedKeyID === scannedKeyID) {
    return { outcome: 'same', scannedKeyID, servedKeyID };
  }
  return {
    outcome: 'differs',
    scannedKeyID,
    servedKeyID,
    reason: 'The key this app was given does not match the one you scanned.',
  };
}
