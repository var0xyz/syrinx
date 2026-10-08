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

/** A thread held whole: its record and every part, in order. */
export interface HeldThread {
  record: ThreadBundle['record'];
  reeds: ThreadBundle['reeds'];
}

/** The thread as held locally, or null unless the record and every part are. */
export async function getLocalThread(threadId: string): Promise<HeldThread | null> {
  const record = await threadsRepository.get(threadId);
  if (!record) return null;
  const reeds = await threadsRepository.getParts(threadId);
  return threadBundleMismatch(threadId, record, reeds) ? null : { record, reeds };
}

/** The whole thread, from this device when held, else fetched as one bundle. */
export async function loadThread(threadId: string): Promise<HeldThread> {
  const local = await getLocalThread(threadId);
  if (local) return local;
  await serverConnection.requestThreadContent(threadId);
  const fetched = await getLocalThread(threadId);
  if (!fetched) throw new Error('thread_incomplete');
  return fetched;
}

/** reed if it matches, else for a thread part the first part of its thread
 * that does, fetching the thread when needed; null when none does. */
export async function firstPartMatching(
  reed: ThreadBundle['reeds'][number],
  matches: (part: ThreadBundle['reeds'][number]) => boolean
): Promise<ThreadBundle['reeds'][number] | null> {
  if (matches(reed)) return reed;
  if (!reed.thread) return null;
  try {
    const thread = await loadThread(reed.thread.head);
    return thread.reeds.find(matches) ?? null;
  } catch {
    return null;
  }
}
