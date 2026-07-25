<script lang="ts">
  import { onMount } from 'svelte';
  import { afterNavigate } from '$app/navigation';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import ServerUnreachableIndicator from '$lib/components/ServerUnreachableIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA, isOnline } from '$lib/services/pwa';
  import { refreshServerInfo } from '$lib/services/serverInfo';
  import { authService } from '$lib/services/auth';
  import { enforceImportGate } from '$lib/services/restoreFlow';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { dbService } from '$lib/services/db';
  import { reedsService, dispatchReedToQueue, initFollowcastIds, prependFollowcastId, removeBroadcastReed } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
  import { pendingRemovalRepository } from '$lib/repositories/pendingRemoval';
  import { verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';

  // TODO: the echoing/replying header currently encodes only "authorId!reedId", which loses
  // the server dimension. Once we have federated reeds, this needs to become a full
  // server/user/reed hierarchy so we can route requests to the correct origin server.
  // A potential format: "userID@serverID/reedID"
  async function requestReferencedReeds(reed: any) {
    const refs = [reed.echoing, reed.replying].filter(Boolean);
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
    refreshServerInfo();
    if (authService.isLoggedIn()) {
      authService.getCurrentUser().then(currentUser => {
        if (currentUser) {
          reedsService.processUnsignedReeds();
          followingRepository.syncPending();
          pendingRevocationRepository.syncPending();
          pendingRemovalRepository.syncPending();
        }
      });
    }
  } else if (!$isOnline) {
    wasOnline = false;
  }

  afterNavigate(({ to }) => {
    if (to) enforceImportGate(to.url.pathname);
  });

  onMount(async () => {
    initializePWA();
    refreshServerInfo();
    enforceImportGate(window.location.pathname);

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

      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(eventId);
        removeBroadcastReed(reed.id);
        dispatchReedToQueue(reed, ServerEvent.DataResponse);
        prependFollowcastId(reed.id);
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid reed signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.BroadcastReed, async (data) => {
      // Broadcast reeds are ephemeral: never stored in IndexedDB.
      // Followed authors belong in followcast only — ignore if we follow them.
      const reed = data.data;
      if (reed?.userID && (await followingRepository.isFollowing(reed.userID))) {
        return;
      }
      dispatchReedToQueue(reed, 'broadcast_reed');
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
        reedsService.processUnsignedReeds();
        followingRepository.syncPending();
        pendingRevocationRepository.syncPending();
        pendingRemovalRepository.syncPending();
        serverConnection.connect()
          .then(() => serverConnection.syncRequest())
          .catch(err => console.error('ServerConnection failed:', err));
      }
    }
  });
</script>

<header>
  <h1><a href={headerLink}>💫 Syrinx</a></h1>
</header>

<ServerUnreachableIndicator />
<OfflineIndicator />
<slot />

<Notifications />
<InstallButton />
