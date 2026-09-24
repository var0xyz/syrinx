/**
 * Holder-side encrypt / requester-side decrypt for reed relay. The server
 * relays ciphertext blindly — it never sees reed content, in transit or
 * otherwise. See docs/content_privacy.md.
 */

import { requestSigner } from './request-signer';
import { cryptoService } from './crypto';
import { apiService } from './api';
import { serverConnection } from './serverConnection';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { resolvePublicKeyArmor } from '$lib/verifiers';
import type { ReedType } from '$lib/types/reed';

/**
 * Encrypt a locally-held reed for the requester named in RELAY_REQUEST.
 * Null means report RELAY_ERROR, not RELAY_MISS: the content is fine,
 * only the key resolution failed.
 */
export async function encryptReedForRequester(
  reed: ReedType,
  requesterID: string
): Promise<string | null> {
  let activeKeyID: string;
  try {
    const info = await apiService.getUserInfo(requesterID);
    if (!info.activeKeyID) return null;
    activeKeyID = info.activeKeyID;
  } catch (error) {
    console.error('Relay: failed to fetch requester key id', requesterID, error);
    return null;
  }

  const armor = await resolvePublicKeyArmor(requesterID, activeKeyID);
  if (!armor) return null;

  return cryptoService.encryptToRecipient(JSON.stringify(reed), armor);
}

/**
 * Decrypt a relayed reed payload with the caller's own active key. Throws
 * on any failure — callers should report 'decrypt_failed' and not store.
 */
export async function decryptRelayPayload(ciphertext: string): Promise<ReedType> {
  const plaintext = await requestSigner.decryptOwn(ciphertext);
  return JSON.parse(plaintext) as ReedType;
}

/** Reports a decrypt failure the same way as any other content rejection. */
export function reportDecryptFailure(storeName: string): void {
  serverConnection.sendContentRejected(storeName, 'decrypt_failed');
}
