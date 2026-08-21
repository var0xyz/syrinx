/**
 * Verify a server countersignature over a caller-built payload.
 *
 * Every signed resource carries the same `serverSignature` block. Rebuild
 * that resource's canonical bytes, then call `verify(serverSignature, payload)`.
 *
 * Reads the signing key from the local publicKeys cache first. The local
 * server's own key is bootstrapped into that cache at startup (see
 * serverInfo.ts's ensureServerKeyCached, which runs right after
 * /server/info resolves, before anything needs to verify), so that's the
 * common case. A peer's or a historical key falls back to the
 * authenticated GET /keys/{id} lookup and gets cached for next time.
 */

import type { ServerSignature } from '$lib/types/api';
import { apiService } from './api';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { formatServerKeyId } from '$lib/utils/keyId';
import type { PublicKey } from '$lib/types/api';

export type VerifyResult =
  | { ok: true }
  | { ok: false; reason: string; detail?: string };

/** RFC3339 UTC whole-seconds, matching what the server puts in signed headers. */
export function signedAtHeader(timestamp: string | Date): string {
  const d = typeof timestamp === 'string' ? new Date(timestamp) : timestamp;
  const ms = d.getTime();
  return new Date(ms - (ms % 1000)).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

async function resolveServerKeyArmor(id: string): Promise<string | null> {
  const cached = await dbService.get<PublicKey>('publicKeys', id);
  if (cached?.armor) return cached.armor;
  try {
    const key = await apiService.getPublicKey(id);
    if (!key?.armor) return null;
    const { allowUnsigned } = await import('$lib/verifiers');
    await dbService.put('publicKeys', key, allowUnsigned);
    return key.armor;
  } catch {
    return null;
  }
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
    const id = formatServerKeyId(serverSignature.fingerprint, serverSignature.serverID);
    const armor = await resolveServerKeyArmor(id);
    if (!armor) {
      return { ok: false, reason: 'server_key_unavailable', detail: serverSignature.fingerprint };
    }

    const signedAt = signedAtHeader(serverSignature.timestamp);
    const valid = await cryptoService.verifySignature(
      payload,
      atob(serverSignature.armor),
      armor,
      signedAt
    );
    if (!valid) {
      return { ok: false, reason: 'signature_invalid' };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: 'error', detail: err instanceof Error ? err.message : String(err) };
  }
}
