/**
 * Holder-side encrypt / requester-side decrypt for reed relay. The server
 * relays ciphertext blindly — it never sees reed content, in transit or
 * otherwise. See docs/content_privacy.md.
 */

import { requestSigner } from './request-signer';
import { cryptoService } from './crypto';
import { serverConnection } from './serverConnection';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { resolvePublicKeyArmor } from '$lib/verifiers';
import { contradictingVouch } from './vouches';
import { parseKeyId } from '$lib/utils/identityRef';
import type { ReedType } from '$lib/types/reed';
import type { ThreadRecord } from '$lib/types/api';

/**
 * Encrypt a locally-held reed to the key RELAY_REQUEST names; the key's
 * owner is the requester. Null means report RELAY_ERROR, not RELAY_MISS:
 * the content is fine, only the key could not be used.
 */
export async function encryptReedForRequester(
  reed: ReedType,
  keyID: string
): Promise<string | null> {
  return encryptForRequester(reed, keyID);
}

/** A whole thread, as a holder relays it: the record and every part in order. */
export interface ThreadBundle {
  record: ThreadRecord;
  reeds: ReedType[];
}

/** Encrypts a thread bundle to the key RELAY_THREAD names. */
export async function encryptThreadForRequester(
  bundle: ThreadBundle,
  keyID: string
): Promise<string | null> {
  return encryptForRequester(bundle, keyID);
}

async function encryptForRequester(payload: unknown, keyID: string): Promise<string | null> {
  const parsed = parseKeyId(keyID);
  if (!parsed) {
    console.error('Relay: request names no usable key', keyID);
    return null;
  }
  const requesterID = `${parsed.userId}@${parsed.serverId}`;

  // A vouch naming a different key contradicts what the server names, so
  // encrypting would hand content to a key someone verified was not theirs.
  // Absence of a vouch is not grounds to refuse.
  const contradiction = await contradictingVouch(requesterID, keyID);
  if (contradiction) {
    console.error(
      'Relay: refusing to encrypt, a vouch names a different key',
      requesterID,
      { vouched: contradiction.subjectKeyId, named: keyID }
    );
    return null;
  }

  const armor = await resolvePublicKeyArmor(requesterID, keyID);
  if (!armor) return null;

  return cryptoService.encryptToRecipient(JSON.stringify(payload), armor);
}

/**
 * Decrypt a relayed reed payload with the caller's own active key. Throws
 * on any failure — callers should report 'decrypt_failed' and not store.
 */
export async function decryptRelayPayload(ciphertext: string): Promise<ReedType> {
  const plaintext = await requestSigner.decryptOwn(ciphertext);
  return JSON.parse(plaintext) as ReedType;
}

/** Decrypts a relayed thread bundle with the caller's own key. Throws on failure. */
export async function decryptThreadBundle(ciphertext: string): Promise<ThreadBundle> {
  const plaintext = await requestSigner.decryptOwn(ciphertext);
  return JSON.parse(plaintext) as ThreadBundle;
}

/** Reports a decrypt failure the same way as any other content rejection. */
export function reportDecryptFailure(storeName: string): void {
  serverConnection.sendContentRejected(storeName, 'decrypt_failed');
}
