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

/** RFC3339 UTC whole-seconds, matching what the server puts in signed headers. */
export function signedAtHeader(timestamp: string | Date): string {
  const d = typeof timestamp === 'string' ? new Date(timestamp) : timestamp;
  const ms = d.getTime();
  return new Date(ms - (ms % 1000)).toISOString().replace(/\.\d{3}Z$/, 'Z');
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

  try {
    const serverPub = await apiService.getServerPublicKey(serverSignature.fingerprint);
    if (!serverPub?.armor) {
      return { ok: false, reason: 'server_key_unavailable', detail: serverSignature.fingerprint };
    }

    const valid = await cryptoService.verifySignature(
      payload,
      atob(serverSignature.armor),
      serverPub.armor
    );
    if (!valid) {
      return { ok: false, reason: 'signature_invalid' };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: 'error', detail: err instanceof Error ? err.message : String(err) };
  }
}
