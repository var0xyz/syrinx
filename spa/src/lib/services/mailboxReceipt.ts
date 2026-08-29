import { authService } from './auth';
import { cryptoService } from './crypto';
import { mailboxRepository, type MailboxCategory } from '$lib/repositories/mailbox';
import { privateKeyRepository } from '$lib/repositories/privateKey';

interface MailboxPayload {
  kind: string;
  category: MailboxCategory;
  message: string;
  link?: string;
  senderUserID: string;
  meta?: unknown;
}

/**
 * Decrypts and stores one MAILBOX WS delivery, returning whether it should
 * be ACKed. A failed decrypt must NOT be ACKed — the server keeps the row
 * and redelivers on the next catch-up rather than the message being
 * silently lost (see specs/notifications/04, 05).
 */
export async function receiveMailboxMessage(id: string, ciphertext: string): Promise<boolean> {
  const fingerprint = authService.getActiveKeyFingerprint();
  const passphrase = authService.getPassphrase();
  if (!fingerprint || !passphrase) {
    console.error('Mailbox: active key or passphrase not available, cannot decrypt', id);
    return false;
  }

  const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
  if (!privateKey?.armor) {
    console.error('Mailbox: private key not found, cannot decrypt', id);
    return false;
  }

  let payload: MailboxPayload;
  try {
    const plaintext = await cryptoService.decryptOwnMessage(ciphertext, privateKey.armor, passphrase);
    payload = JSON.parse(plaintext);
  } catch (error) {
    console.error('Mailbox: failed to decrypt/parse message, will redeliver on next catch-up', id, error);
    return false;
  }

  await mailboxRepository.put({
    id,
    kind: payload.kind,
    // Falls back to 'system' for a row encrypted before Category existed
    // (or any producer that forgets it) — never silently invisible in
    // both tabs.
    category: payload.category === 'interaction' ? 'interaction' : 'system',
    message: payload.message,
    link: payload.link,
    senderUserID: payload.senderUserID,
    meta: payload.meta,
    isRead: false,
    createdAt: new Date().toISOString(),
  });

  return true;
}
