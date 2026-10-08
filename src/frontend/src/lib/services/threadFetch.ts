import { serverConnection } from './serverConnection';
import { decryptThreadBundle, encryptThreadForRequester, type ThreadBundle } from './relayDecrypt';
import { reedsService } from '$lib/repositories/reeds';
import { threadsRepository } from '$lib/repositories/threads';
import { verifyReed, verifyThreadRecord } from '$lib/verifiers';
import { threadBundleMismatch } from '$lib/utils/threadBundle';

/**
 * Holder side of RELAY_THREAD: relays the record and every part as one
 * bundle, or RELAY_MISS when any of them isn't held here. Never part of one.
 */
export async function serveThreadRelay(eventId: string, threadId: string, keyId: string): Promise<void> {
  const record = await threadsRepository.get(threadId);
  const reeds = record ? await threadsRepository.getParts(threadId) : [];
  if (!record || threadBundleMismatch(threadId, record, reeds)) {
    serverConnection.sendRelayMiss(eventId);
    return;
  }
  const ciphertext = await encryptThreadForRequester({ record, reeds }, keyId);
  if (!ciphertext) {
    serverConnection.sendRelayError(eventId);
    return;
  }
  serverConnection.sendRelayResponse(eventId, ciphertext);
}

/** Every check a bundle must pass before any of it is stored. */
async function bundleVerifies(threadId: string, bundle: ThreadBundle): Promise<boolean> {
  if (!bundle?.record || !Array.isArray(bundle.reeds)) return false;
  if (await threadsRepository.isRemoved(threadId)) {
    console.warn('Thread bundle rejected: the thread was removed', threadId);
    return false;
  }
  const mismatch = threadBundleMismatch(threadId, bundle.record, bundle.reeds);
  if (mismatch) {
    console.warn('Thread bundle rejected:', threadId, mismatch);
    return false;
  }
  if (!(await verifyThreadRecord(bundle.record))) return false;
  for (const reed of bundle.reeds) {
    if (!(await verifyReed(reed))) return false;
  }
  return true;
}

/**
 * Requester side: a DATA_RESPONSE to REQUEST_THREAD. Stores the thread only
 * once the record and every part verify and agree, then acks it whole.
 */
export async function receiveThreadResponse(data: {
  id: string;
  request_id: string;
  reed_id: string;
  ciphertext: string;
}): Promise<void> {
  let bundle: ThreadBundle;
  try {
    bundle = await decryptThreadBundle(data.ciphertext);
  } catch (error) {
    console.warn('Thread response failed to decrypt:', data.reed_id, error);
    serverConnection.sendContentRejected('threads', 'decrypt_failed');
    serverConnection.sendDataInvalid(data.id);
    serverConnection.rejectPendingThreadRequest(data.request_id, error);
    return;
  }
  if (!(await bundleVerifies(data.reed_id, bundle))) {
    serverConnection.sendDataInvalid(data.id);
    serverConnection.rejectPendingThreadRequest(data.request_id, new Error('thread_invalid'));
    return;
  }
  try {
    for (const reed of bundle.reeds) await reedsService.storeReed(reed);
    await threadsRepository.put(bundle.record);
  } catch (error) {
    console.warn('Thread failed to store:', data.reed_id, error);
    serverConnection.sendDataInvalid(data.id);
    serverConnection.rejectPendingThreadRequest(data.request_id, error);
    return;
  }
  serverConnection.sendDataAck(data.id);
  serverConnection.resolvePendingThreadRequest(data.request_id, bundle.reeds);
}
