<script lang="ts">
  import { onMount } from 'svelte';
  import { afterNavigate } from '$app/navigation';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import ServerUnreachableIndicator from '$lib/components/ServerUnreachableIndicator.svelte';
  import UpdateAvailableIndicator from '$lib/components/UpdateAvailableIndicator.svelte';
  import { initializePWA, onReconnect } from '$lib/services/pwa';
  import { refreshServerInfo } from '$lib/services/serverInfo';
  import { authService } from '$lib/services/auth';
  import { ensureDeviceId } from '$lib/services/deviceId';
  import { enforceImportGate } from '$lib/services/restoreFlow';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { dbService } from '$lib/services/db';
  import { reedsService, dispatchReedToQueue, initFollowIds, prependFollowId, removeBroadcastReed } from '$lib/repositories/reeds';
  import { reedRequestsRepository } from '$lib/repositories/reedRequests';
  import { followingRepository } from '$lib/repositories/following';
  import { clearReedRequestDispatched, startReedRequestDrainer } from '$lib/services/reedRequestDrainer';
  import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
  import { pendingRemovalRepository } from '$lib/repositories/pendingRemoval';
  import { pendingLikeRepository } from '$lib/repositories/pendingLike';
  import { pendingUnlikeRepository } from '$lib/repositories/pendingUnlike';
  import { pendingPublicationRepository } from '$lib/repositories/pendingPublication';
  import { syncPendingBackupEvents } from '$lib/services/backupMetrics';
  import { verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { isBlankEcho } from '$lib/utils/emptyEcho';

  // Prefetch reeds referenced by echoing/replying (userID@serverID/reedID).
  async function requestReferencedReeds(reed: any) {
    const refs = [reed.echoing, reed.replying].filter(Boolean);
    for (const ref of refs) {
      const parsed = parseReedRef(ref);
      if (!parsed) continue;
      const existing = await reedsService.getReed(parsed.authorId, parsed.reedId);
      if (!existing) {
        serverConnection.requestReedContent(parsed.reedId, parsed.authorId, parsed.serverId);
      }
    }
  }

  let user = null;
  $: headerLink = user ? '/reeds' : '/';

  function syncAfterReconnect() {
    refreshServerInfo();
    if (!authService.isLoggedIn()) return;
    authService.getCurrentUser().then((currentUser) => {
      if (!currentUser) return;
      followingRepository.syncPending();
      pendingRevocationRepository.syncPending();
      pendingRemovalRepository.syncPending();
      pendingLikeRepository.syncPending();
      pendingUnlikeRepository.syncPending();
      syncPendingBackupEvents();
      serverConnection.connect()
        .then(async () => {
          clearReedRequestDispatched();
          serverConnection.syncRequest();
          startReedRequestDrainer();
          await reedsService.announcePublishedReeds();
        })
        .catch((err) => console.error('ServerConnection reconnect failed:', err));
    });
  }

  afterNavigate(({ to }) => {
    if (to) enforceImportGate(to.url.pathname);
  });

  onMount(() => {
    ensureDeviceId();
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', (event) => {
        if (event.key === 'userId' && event.newValue === null && event.oldValue) {
          void authService.clearSession();
        }
      });
    }
  });

  onMount(() => {
    initializePWA();
    const stopReconnect = onReconnect(syncAfterReconnect);
    refreshServerInfo();
    enforceImportGate(window.location.pathname);

    void (async () => {

    // Register WS handlers unconditionally so they're in place whether the
    // connection is established now (existing user) or later (post-signup).
    serverConnection.on(ServerEvent.PublishReadyAck, ({ reed_id }) => {
      if (!reed_id) return;
      void pendingPublicationRepository.delete(reed_id);
    });
    serverConnection.on(ServerEvent.RelayRequest, async ({ event_id, reed_id }) => {
      console.log('ServerConnection: relay request received for reed:', reed_id, 'event:', event_id);
      const reed = await dbService.get('reeds', reed_id);
      if (reed) {
        console.log('ServerConnection: reed found in IndexedDB, fulfilling relay:', reed_id);
        serverConnection.sendRelayResponse(event_id, reed);
      } else {
        console.warn('ServerConnection: reed NOT found in IndexedDB, sending relay miss:', reed_id);
        serverConnection.sendRelayMiss(event_id);
      }
    });
    serverConnection.on(ServerEvent.DataResponse, async (data) => {
      const reed = data.data;
      const eventId = data.event_id;
      const requestId = data.request_id as string | undefined;

      try {
        await reedsService.storeReed(reed);
        if (requestId) await reedRequestsRepository.delete(requestId);
        serverConnection.sendDataAck(eventId);
        removeBroadcastReed(reed.id);
        // Explicit REQUEST_REED or profile_subscription relay reply.
        dispatchReedToQueue(reed, ServerEvent.DataResponse);
        if (reed.userID && (await followingRepository.isFollowing(reed.userID))) {
          prependFollowId(reed.id);
        }
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid reed signature, rejecting:', reed.id, error);
        if (requestId) await reedRequestsRepository.delete(requestId);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.FollowReed, async (data) => {
      const reed = data.data;
      const eventId = data.event_id;

      try {
        await reedsService.storeReed(reed);
        if (eventId) serverConnection.sendDataAck(eventId);
        removeBroadcastReed(reed.id);
        prependFollowId(reed.id);
        dispatchReedToQueue(reed, 'follow_reed');
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid follow reed signature, rejecting:', reed?.id, error);
        if (eventId) serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.PipeReed, async (data) => {
      const reed = data.data;
      const eventId = data.event_id;

      try {
        await reedsService.storeReed(reed);
        if (eventId) serverConnection.sendDataAck(eventId);
        removeBroadcastReed(reed.id);
        dispatchReedToQueue(reed, 'pipe_reed');
        // Also following the author: keep the follow feed in sync without a second relay.
        if (reed.userID && (await followingRepository.isFollowing(reed.userID))) {
          prependFollowId(reed.id);
          dispatchReedToQueue(reed, 'follow_reed');
        }
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid pipe reed signature, rejecting:', reed?.id, error);
        if (eventId) serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.BroadcastReed, async (data) => {
      // Broadcast reeds are ephemeral: never stored in IndexedDB.
      // Followed authors belong in the follow feed only — ignore if we follow them.
      const reed = data.data;
      if (isBlankEcho(reed)) return;
      if (reed?.userID && (await followingRepository.isFollowing(reed.userID))) {
        return;
      }
      dispatchReedToQueue(reed, 'broadcast_reed', data.username);
    });
    serverConnection.on(ServerEvent.ReedRemoved, async (data) => {
      const eventId = data.event_id;
      const cert = data.data;
      if (!cert || cert.type !== 'reed') {
        console.warn('ServerConnection: ignoring non-reed removal cert', cert?.type);
        if (eventId) serverConnection.sendDataInvalid(eventId);
        return;
      }
      if (await verifyAndCommitReedRemoval(cert)) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: reed removal cert failed verification:', cert.reedID);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.AccountRemoved, async (data) => {
      const eventId = data.event_id;
      const cert = data.data;
      if (!cert || cert.type !== 'account') {
        console.warn('ServerConnection: ignoring non-account removal cert', cert?.type);
        if (eventId) serverConnection.sendDataInvalid(eventId);
        return;
      }
      if (await verifyAndCommitAccountRemoval(cert)) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: account removal cert failed verification:', cert.userID);
        serverConnection.sendDataInvalid(eventId);
      }
    });

    // Check authentication status for header. Mid-recovery has local identity
    // but is not a finished session — do not connect or treat as logged in.
    if (authService.isLoggedIn()) {
      user = await authService.getCurrentUser();

      if (user) {
        reedsService.processUnsignedReeds().then(() =>
          serverConnection.connect()
            .then(async () => {
              clearReedRequestDispatched();
              serverConnection.syncRequest();
              startReedRequestDrainer();
              await reedsService.announcePublishedReeds();
            })
            .catch(err => console.error('ServerConnection failed:', err))
        );
        followingRepository.syncPending();
        pendingRevocationRepository.syncPending();
        pendingRemovalRepository.syncPending();
      pendingLikeRepository.syncPending();
      pendingUnlikeRepository.syncPending();
        syncPendingBackupEvents();
      }
    }
    })();

    return () => {
      stopReconnect();
    };
  });
</script>

<UpdateAvailableIndicator />

<header>
  <h1><a href={headerLink}>💫 Syrinx</a></h1>
</header>

<ServerUnreachableIndicator />
<OfflineIndicator />
<slot />

<Notifications />
