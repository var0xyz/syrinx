/**
 * Verify a server countersignature over a caller-built payload.
 *
 * Every signed resource carries the same `server` block. Rebuild that
 * resource's canonical bytes, then call `verify(server, payload)`.
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

export async function verify(server: ServerSignature | null | undefined, payload: string): Promise<VerifyResult> {
  if (!server?.id || !server.fingerprint || !server.signature || !server.timestamp) {
    return { ok: false, reason: 'missing_fields' };
  }
  if (!payload) {
    return { ok: false, reason: 'missing_fields', detail: 'payload' };
  }

  try {
    const serverPub = await apiService.getServerPublicKey(server.fingerprint);
    if (!serverPub?.armor) {
      return { ok: false, reason: 'server_key_unavailable', detail: server.fingerprint };
    }

    const valid = await cryptoService.verifySignature(payload, atob(server.signature), serverPub.armor);
    if (!valid) {
      return { ok: false, reason: 'signature_invalid' };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: 'error', detail: err instanceof Error ? err.message : String(err) };
  }
}
