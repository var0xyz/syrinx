<script lang="ts">
  import { onMount } from 'svelte';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA, isOnline } from '$lib/services/pwa';
  import { authService } from '$lib/services/auth';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { dbService } from '$lib/services/db';
  import { reedsService, dispatchReedToQueue, initFollowcastIds, prependFollowcastId } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
  import { chatRepository } from '$lib/repositories/chat';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import { publicKeyRepository } from '$lib/repositories/publicKey';
  import { pendingChatCount } from '$lib/stores/chat';
  import { notificationStore } from '$lib/stores/notifications';

  // TODO: the echoing/replying header currently encodes only "authorId!reedId", which loses
  // the server dimension. Once we have federated reeds, this needs to become a full
  // server/user/reed hierarchy so we can route requests to the correct origin server.
  // A potential format: "userID@serverID/reedID"
  async function requestReferencedReeds(reed: any) {
    const refs = [reed.headers?.echoing, reed.headers?.replying].filter(Boolean);
    for (const ref of refs) {
      const sep = ref.lastIndexOf('!');
      if (sep === -1) continue;
      const authorId = ref.substring(0, sep);
      const reedId = ref.substring(sep + 1);
      const existing = await reedsService.getReed(authorId, reedId);
      if (!existing) {
        serverConnection.requestReedContent(reedId, authorId, authorId);
      }
    }
  }

  let user = null;
  $: headerLink = user ? '/reeds' : '/';

  let wasOnline = false;
  $: if ($isOnline && !wasOnline) {
    wasOnline = true;
    authService.getCurrentUser().then(currentUser => {
      if (currentUser) {
        reedsService.processUnsignedReeds();
        followingRepository.syncPending();
        pendingRevocationRepository.syncPending();
      }
    });
  } else if (!$isOnline) {
    wasOnline = false;
  }

  onMount(async () => {
    initializePWA();

    // Register WS handlers unconditionally so they're in place whether the
    // connection is established now (existing user) or later (post-signup).
    serverConnection.on(ServerEvent.RelayRequest, async ({ event_id, reed_id }) => {
      console.log('ServerConnection: relay request received for reed:', reed_id, 'event:', event_id);
      serverConnection.storePendingRelayRequest(reed_id, event_id);
      const reed = await dbService.get('reeds', reed_id);
      if (reed) {
        console.log('ServerConnection: reed found in IndexedDB, fulfilling relay:', reed_id);
        serverConnection.fulfillPendingRelayRequest(reed_id, reed);
      } else {
        console.warn('ServerConnection: reed NOT found in IndexedDB, sending relay miss:', reed_id);
        serverConnection.sendRelayMiss(event_id);
      }
    });
    serverConnection.on(ServerEvent.DataResponse, async (data) => {
      const reed = data.data;
      const eventId = data.event_id;

      if (await reedsService.validateReed(reed)) {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(eventId);
        dispatchReedToQueue(reed, ServerEvent.DataResponse);
        prependFollowcastId(reed.headers.id);
        await requestReferencedReeds(reed);
      } else {
        console.warn('ServerConnection: invalid reed signature, rejecting:', reed.headers.id);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.BroadcastReed, (data) => {
      // Broadcast reeds are ephemeral: never stored in IndexedDB.
      dispatchReedToQueue(data.data, 'broadcast_reed');
    });

    // Check authentication status for header
    user = await authService.getCurrentUser();

    if (user) {
      reedsService.processUnsignedReeds();
      followingRepository.syncPending();
      pendingRevocationRepository.syncPending();
      chatRepository.pendingCount().then(n => pendingChatCount.set(n));
      serverConnection.connect()
        .then(() => {
          serverConnection.on(ServerEvent.RelayRequest, async ({ event_id, reed_id }) => {
            console.log('ServerConnection: relay request received for reed:', reed_id, 'event:', event_id);
            serverConnection.storePendingRelayRequest(reed_id, event_id);
            const reed = await dbService.get('reeds', reed_id);
            if (reed) {
              console.log('ServerConnection: reed found in IndexedDB, fulfilling relay:', reed_id);
              serverConnection.fulfillPendingRelayRequest(reed_id, reed);
            } else {
              console.warn('ServerConnection: reed NOT found in IndexedDB, relay pending:', reed_id);
            }
          });
          serverConnection.on(ServerEvent.DataResponse, async (data) => {
            await reedsService.storeReed(data.data);
            dispatchReedToQueue(data.data, ServerEvent.DataResponse);
            prependFollowcastId(data.data.headers.id);
            await requestReferencedReeds(data.data);
          });
          serverConnection.on(ServerEvent.BroadcastReed, (data) => {
            // Broadcast reeds are ephemeral: never stored in IndexedDB.
            dispatchReedToQueue(data.data, 'broadcast_reed');
          });
          serverConnection.on(ServerEvent.ChatRequest, async ({ chatId, senderId, message }) => {
            const existing = await chatRepository.get(chatId);
            if (existing) return;
            await chatRepository.put({ id: chatId, userId: senderId, confirmed: false });
            await chatRepository.putMessage({ id: chatId, clientId: chatId, chatId, authorId: senderId, content: message, status: 'delivered', createdAt: Date.now() });
            pendingChatCount.update(n => n + 1);
          });
          serverConnection.on(ServerEvent.ChatRequestAccepted, async ({ chatId }) => {
            const chat = await chatRepository.get(chatId);
            if (!chat) return;
            await chatRepository.put({ ...chat, confirmed: true, pending: false });
            const other = await userRepository.getByUserId(chat.userId).catch(() => null);
            const name = other?.username ?? chat.userId;
            notificationStore.success(`${name} accepted your chat request`);
          });
          serverConnection.on(ServerEvent.ChatMessage, async ({ serverId, clientId, chatId, senderId, content, createdAt }) => {
            const existing = await chatRepository.getMessage(serverId);
            if (existing) return;
            await chatRepository.putMessage({ id: serverId, clientId, chatId, authorId: senderId, content, status: 'delivered', createdAt: new Date(createdAt).getTime() });
            await apiService.ackChatMessage(serverId);
            serverConnection.confirmDelivery(serverId, senderId);
          });
          serverConnection.on(ServerEvent.ChatDeliveryConfirmation, async ({ messageId }) => {
            await chatRepository.updateMessageStatus(messageId, 'delivered');
          });
          serverConnection.on(ServerEvent.ChatSigVerifyFailed, async ({ messageId }) => {
            await chatRepository.updateMessageStatus(messageId, 'failed');
          });
          serverConnection.on(ServerEvent.BlockEvent, async ({ blockerId }) => {
            const userRecord = await userRepository.getByUserId(blockerId);
            if (userRecord?.fingerprint) {
              await publicKeyRepository.deletePublicKey(userRecord.fingerprint);
            }
            await reedsService.deleteReedsByAuthor(blockerId);
            await userRepository.delete(blockerId);
            serverConnection.ackBlockEvent(blockerId);
          });
          serverConnection.syncRequest();
        })
        .catch(err => console.error('ServerConnection failed:', err));
    }
  });
</script>

<header>
  <h1><a href={headerLink}>💫 Syrinx</a></h1>
</header>

<OfflineIndicator />
<slot />

<Notifications />
<InstallButton />
