import { requestSigner } from './request-signer';
import { mailboxRepository } from '$lib/repositories/mailbox';

interface MailboxPayload {
  kind: string;
  message: string;
  link?: string;
  meta?: unknown;
}

/**
 * Decrypts and stores one MAILBOX WS delivery, returning whether it should
 * be ACKed. A failed decrypt must NOT be ACKed — the server keeps the row
 * and redelivers on the next catch-up rather than the message being
 * silently lost.
 */
export async function receiveMailboxMessage(id: string, ciphertext: string): Promise<boolean> {
  let payload: MailboxPayload;
  try {
    const plaintext = await requestSigner.decryptOwn(ciphertext);
    payload = JSON.parse(plaintext);
  } catch (error) {
    console.error('Mailbox: failed to decrypt/parse message, will redeliver on next catch-up', id, error);
    return false;
  }

  await mailboxRepository.put({
    id,
    kind: payload.kind,
    message: payload.message,
    link: payload.link,
    meta: payload.meta,
    isRead: false,
    createdAt: new Date().toISOString(),
  });

  return true;
}
