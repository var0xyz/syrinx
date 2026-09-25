import { apiService } from './api';
import { cryptoService } from './crypto';
import { verifyPublicKey } from '$lib/verifiers';
import { parseKeyId, formatKeyId, appendFingerprint } from '$lib/utils/identityRef';
import type * as api from '$lib/types/api';

/**
 * Outcome of comparing a scanned key id against the key we resolved. There
 * is no "first contact" case: the scanner always fetches and derives, so
 * there is always something to compare. 'unresolvable' is not a comparison
 * result but a blocked flow.
 */
export type CompareOutcome = 'same' | 'differs' | 'unresolvable';

export interface CompareResult {
  outcome: CompareOutcome;
  /** The id the subject showed, which is what a vouch would attest. */
  scannedKeyID: string;
  /** The id we derived from the armor the server served, if any. */
  servedKeyID: string | null;
  /** Set when the flow must not continue. */
  reason?: string;
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
async function resolveServedKeyID(subjectUserID: string): Promise<string | null> {
  let key: api.PublicKey | null = null;
  try {
    const info = await apiService.getUserInfo(subjectUserID);
    if (!info?.activeKeyID) return null;
    key = await apiService.getPublicKey(info.activeKeyID);
  } catch (error) {
    console.error('[vouchVerify] could not resolve served key', subjectUserID, error);
    return null;
  }
  if (!key?.armor) return null;

  // verifyPublicKey re-derives the fingerprint from the armor and rejects a
  // mismatch, so a key that passes here really is the key it claims to be.
  if (!(await verifyPublicKey(key))) {
    console.error('[vouchVerify] served key failed verification', key.id);
    return null;
  }
  const derived = await cryptoService.fingerprintFromArmor(key.armor);
  const parsed = parseKeyId(key.id);
  if (!parsed) return null;
  return formatKeyId(parsed.userId, parsed.serverId, derived);
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

  const servedKeyID = await resolveServedKeyID(subjectUserID);

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
