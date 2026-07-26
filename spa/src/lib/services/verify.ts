/**
 * Verify a server countersignature over a caller-built payload.
 *
 * Every signed resource carries the same `serverSignature` block. Rebuild
 * that resource's canonical bytes, then call `verify(serverSignature, payload)`.
 */

import type { ServerSignature } from '$lib/types/api';
import { apiService } from './api';
import { cryptoService } from './crypto';

export type VerifyResult =
  | { ok: true }
  | { ok: false; reason: string; detail?: string };

/**
 * RFC3339 UTC whole-seconds, matching Go `time.RFC3339` after Truncate(Second).
 *
 * Prefer the wire string when it is already canonical so we never round-trip
 * through `Date` (Safari is stricter about fractional seconds / offsets).
 */
export function signedAtHeader(timestamp: string | Date): string {
  if (typeof timestamp === 'string') {
    if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(timestamp)) {
      return timestamp;
    }
    // Safari Date.parse often rejects >3 fractional digits; trim before parse.
    const normalized = timestamp
      .replace(
        /(\d{2}:\d{2}:\d{2})\.(\d{3})\d*(Z|[+-]\d{2}:?\d{2})$/,
        '$1.$2$3'
      )
      .replace(/(\d{2}:\d{2}:\d{2})\.\d+(Z|[+-]\d{2}:?\d{2})$/, '$1$2');
    const d = new Date(normalized);
    const ms = d.getTime();
    if (Number.isNaN(ms)) {
      throw new Error(`invalid signedAt timestamp: ${timestamp}`);
    }
    return new Date(ms - (ms % 1000)).toISOString().replace(/\.\d{3}Z$/, 'Z');
  }
  const ms = timestamp.getTime();
  if (Number.isNaN(ms)) {
    throw new Error('invalid signedAt Date');
  }
  return new Date(ms - (ms % 1000)).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** Decode serverSignature.armor (base64 of ASCII-armored PGP). */
function decodeSignatureArmor(b64: string): string {
  // Normalize CRLF from some mobile WebViews before handing to OpenPGP.js.
  return atob(b64.replace(/\s/g, '')).replace(/\r\n/g, '\n');
}

export async function verify(
  serverSignature: ServerSignature | null | undefined,
  payload: string
): Promise<VerifyResult> {
  if (
    !serverSignature?.serverID ||
    !serverSignature.fingerprint ||
    !serverSignature.armor ||
    !serverSignature.timestamp
  ) {
    return { ok: false, reason: 'missing_fields' };
  }
  if (!payload) {
    return { ok: false, reason: 'missing_fields', detail: 'payload' };
  }

  let signedAt: string;
  try {
    signedAt = signedAtHeader(serverSignature.timestamp);
  } catch (err) {
    return {
      ok: false,
      reason: 'invalid_timestamp',
      detail: err instanceof Error ? err.message : String(err),
    };
  }

  try {
    const serverPub = await apiService.getServerPublicKey(serverSignature.fingerprint);
    if (!serverPub?.armor) {
      return { ok: false, reason: 'server_key_unavailable', detail: serverSignature.fingerprint };
    }

    let gotFp: string;
    try {
      gotFp = await cryptoService.fingerprintFromArmor(serverPub.armor);
    } catch (err) {
      return {
        ok: false,
        reason: 'server_key_unreadable',
        detail: err instanceof Error ? err.message : String(err),
      };
    }
    if (gotFp.toLowerCase() !== serverSignature.fingerprint.toLowerCase()) {
      return {
        ok: false,
        reason: 'server_key_fingerprint_mismatch',
        detail: `want=${serverSignature.fingerprint}; got=${gotFp}`,
      };
    }

    let sigArmor: string;
    try {
      sigArmor = decodeSignatureArmor(serverSignature.armor);
    } catch (err) {
      return {
        ok: false,
        reason: 'armor_b64_invalid',
        detail: err instanceof Error ? err.message : String(err),
      };
    }

    const result = await cryptoService.verifySignatureDetailed(
      payload,
      sigArmor,
      serverPub.armor
    );
    if (result.ok === false) {
      return {
        ok: false,
        reason: 'signature_invalid',
        detail: `signedAtRaw=${serverSignature.timestamp}; signedAt=${signedAt}; payloadBytes=${new TextEncoder().encode(payload).length}; pgp=${result.error}`,
      };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: 'error', detail: err instanceof Error ? err.message : String(err) };
  }
}
