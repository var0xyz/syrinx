import { get } from 'svelte/store';
import type * as api from '$lib/types/api';
import { create } from '@bufbuild/protobuf';
import { ThreadRecordSchema } from '$lib/proto/common_pb';
import { Reed } from '$lib/types/reed';
import { apiService } from './api';
import { requestSigner } from './request-signer';
import { serverConnection } from './serverConnection';
import { serverInfo } from './serverInfo';
import { isOnline } from './pwa';
import { buildThreadUserPayload } from './signing';
import { clearPublishTipOverride, previousIDForPublish } from './publishTip';
import { pendingPublicationRepository } from '$lib/repositories/pendingPublication';
import { reedsService } from '$lib/repositories/reeds';
import { threadsRepository } from '$lib/repositories/threads';
import { generateReedId } from '$lib/utils/id';
import { canonicalReedId } from '$lib/utils/identityRef';

/**
 * Signs every part and the thread record, has the server countersign them
 * in one request, then stores them and announces the head. Returns the
 * head's ID. Nothing is stored unless the server accepts the whole thread.
 */
export async function publishThread(texts: string[], keyId: string): Promise<string> {
  if (!get(isOnline)) throw new Error("You're offline. Threads can only be published online.");
  const userID = localStorage.getItem('userId') ?? '';
  const serverID = get(serverInfo)?.id || localStorage.getItem('serverId');
  if (!userID || !serverID) throw new Error('Not signed in');

  // The server requires IDs that rise with their index.
  const ids = texts.map(() => canonicalReedId({ userID, id: generateReedId() })).sort();
  const head = ids[0];
  const parts: Reed[] = [];
  for (const [index, text] of texts.entries()) {
    const reed = new Reed();
    reed.id = ids[index];
    reed.content = text;
    reed.thread = { head, index };
    reed.setUserSignature(keyId, await requestSigner.sign(reed.signedPayload()));
    parts.push(reed);
  }
  const threadSignature = await requestSigner.sign(buildThreadUserPayload(serverID, head, ids));

  const previousID = await previousIDForPublish();
  const response = await apiService.createThread({
    previousId: previousID ?? '',
    threadSignature,
    reeds: parts.map((reed) => ({
      reedId: reed.id,
      signature: reed.userSignature!.armor,
      tags: reed.tags,
      mentions: reed.mentions,
    })),
  });

  for (const [i, reed] of parts.entries()) {
    reed.applyServerResponse(response.reeds[i]);
    await reedsService.storeReed(reed.asObject());
  }
  const record: api.ThreadRecord = create(ThreadRecordSchema, {
    serverId: serverID,
    userId: userID,
    threadId: head,
    reedIds: ids,
    userSignature: { id: keyId, armor: threadSignature },
    serverSignature: response.serverSignature,
  });
  await threadsRepository.put(record);
  clearPublishTipOverride();

  await pendingPublicationRepository.put(head);
  await serverConnection.publishReady(head, { broadcast: true });
  return head;
}
