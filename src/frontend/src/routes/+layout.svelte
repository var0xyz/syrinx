<script context="module" lang="ts">
  // Module-level, not per-instance: guards against double-registering the
  // WS handlers below if this root layout's onMount ever runs more than
  // once (e.g. dev-mode HMR) — serverConnection.on() has no dedup of its
  // own, so a second registration would double-deliver every WS event.
  let wsHandlersRegistered = false;
</script>

<script lang="ts">
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { afterNavigate } from '$app/navigation';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import ServerUnreachableIndicator from '$lib/components/ServerUnreachableIndicator.svelte';
  import ServerIdMismatchIndicator from '$lib/components/ServerIdMismatchIndicator.svelte';
  import UpdateAvailableIndicator from '$lib/components/UpdateAvailableIndicator.svelte';
  import { initializePWA, onReconnect } from '$lib/services/pwa';
  import { syncPendingEvictions } from '$lib/services/eviction';
  import { pendingVouchesRepository } from '$lib/repositories/pendingVouches';
  import { ingestPushedVouch } from '$lib/services/vouches';
  import { refreshServerInfo, serverKeyCompromise, serverKeyRejected } from '$lib/services/serverInfo';
  import { hasTrustedServerKey } from '$lib/services/serverKeyTrust';
  import ServerKeyGate from '$lib/components/ServerKeyGate.svelte';
  import { authService } from '$lib/services/auth';
  import { ensureDeviceId } from '$lib/services/deviceId';
  import { enforceImportGate } from '$lib/services/restoreFlow';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { create } from '@bufbuild/protobuf';
  import { ThreadRemovalSchema } from '$lib/proto/common_pb';
  import { dbService } from '$lib/services/db';
  import { reedsService, dispatchReedToQueue, removeBroadcastReed } from '$lib/repositories/reeds';
  import { recordActivity } from '$lib/repositories/activity';
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
  import { applyKeyRevocation } from '$lib/services/keyRevocation';
  import { receiveBlock, receiveUnblock } from '$lib/services/blockedBy';
  import { syncPendingBlocks } from '$lib/services/blocks';
  import { setBlockedReporter } from '$lib/services/api';
  import { firstPartMatching, receiveThreadResponse, serveThreadRelay } from '$lib/services/threadFetch';
  import { applyThreadRemoval } from '$lib/services/threadRemoval';
  import { verifyAndStoreMention } from '$lib/services/mentionsSync';
  import { markUnread } from '$lib/stores/unreadInteractions';
  import { notificationStore } from '$lib/stores/notifications';
  import ActivitySidebar from '$lib/components/ActivitySidebar.svelte';
  import { isValidRef } from '$lib/utils/identityRef';
  import { isBlankEcho } from '$lib/utils/emptyEcho';
  import { encryptReedForRequester } from '$lib/services/relayDecrypt';
  import { verifyClaimedTags } from '$lib/verifiers';
  import type { ReedType } from '$lib/types/reed';

  // Prefetch reeds referenced by echoing/replying (userID@serverID/reedID).
  async function requestReferencedReeds(reed: any) {
    const refs = [reed.echoing, reed.replying?.to].filter(Boolean);
    for (const ref of refs) {
      if (!isValidRef(ref)) continue;
      const existing = await reedsService.getReed(ref);
      if (!existing) {
        serverConnection.requestReedContent(ref);
      }
    }
  }

  // Mirrors the /replies list: someone else's reply to a reed of mine.
  async function isReplyToMe(reed: ReedType): Promise<boolean> {
    const myUserID = localStorage.getItem('userId');
    if (!myUserID || !reed.replying?.to || reed.userID === myUserID) return false;
    const parent = await reedsService.getReed(reed.replying.to);
    return parent?.userID === myUserID;
  }

  let user = null;
  $: headerLink = user ? '/reeds' : '/welcome';

  let serverKeyTrusted = hasTrustedServerKey();
  let trustedSessionStarted = false;
  let stopReconnect: (() => void) | undefined;

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
      pendingVouchesRepository.syncPending();
      void syncPendingBlocks();
      syncPendingBackupEvents();
      void syncPendingEvictions();
      serverConnection.reconnect()
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
    if (!serverKeyTrusted) return;
    ensureDeviceId();
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', (event) => {
        if (event.key === 'userId' && event.newValue === null && event.oldValue) {
          void authService.clearSession();
        }
      });
    }
  });

  // Runs on mount, or when the key gate clears mid-session — the gate
  // renders before mount, so setup would otherwise never run on that load.
  function startTrustedSession() {
    if (trustedSessionStarted) return;
    trustedSessionStarted = true;
    initializePWA();
    stopReconnect = onReconnect(syncAfterReconnect);
    refreshServerInfo();
    enforceImportGate(window.location.pathname);

    if (!wsHandlersRegistered) {
    wsHandlersRegistered = true;

    void (async () => {

    // Register WS handlers unconditionally so they're in place whether the
    // connection is established now (existing user) or later (post-signup).
    serverConnection.on(ServerEvent.PublishReadyAck, ({ reedId: reed_id }) => {
      if (!reed_id) return;
      void pendingPublicationRepository.delete(reed_id);
    });
    serverConnection.on(ServerEvent.RelayRequest, async ({ id: eventId, reedId: reed_id, requesterKeyId: requester_key_id }) => {
      console.log('ServerConnection: relay request received for reed:', reed_id, 'event:', eventId);
      const reed = await dbService.get<ReedType>('reeds', reed_id);
      if (!reed) {
        console.warn('ServerConnection: reed NOT found in IndexedDB, sending relay miss:', reed_id);
        serverConnection.sendRelayMiss(eventId);
        return;
      }
      const ciphertext = await encryptReedForRequester(reed, requester_key_id);
      if (!ciphertext) {
        console.warn('ServerConnection: could not encrypt for requester, sending relay error:', requester_key_id);
        serverConnection.sendRelayError(eventId);
        return;
      }
      console.log('ServerConnection: reed found and encrypted, fulfilling relay:', reed_id);
      serverConnection.sendRelayResponse(eventId, ciphertext);
    });
    serverConnection.on(ServerEvent.RelayThread, ({ id: eventId, threadId: thread_id, requesterKeyId: requester_key_id }) => {
      void serveThreadRelay(eventId, thread_id, requester_key_id);
    });
    serverConnection.on(ServerEvent.ThreadRemoved, async (data) => {
      const eventId = data.id;
      const removal = data.cert && data.record ? create(ThreadRemovalSchema, { cert: data.cert, record: data.record }) : null;
      if (removal && (await applyThreadRemoval(removal))) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: thread removal failed verification:', data.cert?.threadId);
        if (eventId) serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.ThreadResponse, (data) => {
      void receiveThreadResponse(data);
    });
    serverConnection.onEncryptedReed(ServerEvent.DataResponse, 'relayed reed', async (reed, data) => {
      const requestId = (data.requestId as string | undefined) || undefined;
      try {
        await reedsService.storeReed(reed);
        if (requestId) {
          await reedRequestsRepository.delete(requestId);
          serverConnection.resolvePendingReedRequest(requestId, reed);
        }
        serverConnection.sendDataAck(data.id);
        removeBroadcastReed(reed.id);
        await recordActivity(reed);
        dispatchReedToQueue(reed, ServerEvent.DataResponse);
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid reed signature, rejecting:', reed.id, error);
        if (requestId) {
          await reedRequestsRepository.delete(requestId);
          serverConnection.rejectPendingReedRequest(requestId, error);
        }
        serverConnection.sendDataInvalid(data.id);
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.FollowReed, 'follow reed', async (reed, data) => {
      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(data.id);
        removeBroadcastReed(reed.id);
        await recordActivity(reed);
        dispatchReedToQueue(reed, 'follow_reed');
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid follow reed signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(data.id);
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.ArchiveReed, 'archive reed', async (reed, data) => {
      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(data.id);
      } catch (error) {
        console.warn('ServerConnection: invalid archive reed signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(data.id);
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.PipeReed, 'pipe reed', async (reed, data) => {
      // A thread is delivered once, as its head: the tag may be in a later part.
      const tag = serverConnection.activePipeTag;
      const tagged = await firstPartMatching(reed, (part) => verifyClaimedTags(part, tag));
      if (!tagged) {
        console.warn('ServerConnection: pipe reed tag claim mismatch, rejecting:', reed.id);
        serverConnection.sendContentRejected('reeds', 'tag_claim_mismatch');
        serverConnection.sendDataInvalid(data.id);
        return;
      }

      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(data.id);
        removeBroadcastReed(reed.id);
        await recordActivity(reed);
        dispatchReedToQueue(tagged, 'pipe_reed');
        // Also following the author: keep the follow feed in sync without a second relay.
        if (reed.userID && (await followingRepository.isFollowing(reed.userID))) {
          dispatchReedToQueue(reed, 'follow_reed');
        }
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid pipe reed signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(data.id);
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.ReedReply, 'reed reply', async (reed, data) => {
      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(data.id);
        removeBroadcastReed(reed.id);
        await recordActivity(reed);
        dispatchReedToQueue(reed, 'reed_reply');
        // Thread subscribers get REED_REPLY too; only flag what /replies lists.
        if (await isReplyToMe(reed)) markUnread('replies');
        await requestReferencedReeds(reed);
      } catch (error) {
        console.warn('ServerConnection: invalid reed reply signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(data.id);
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.Mentioned, 'mention', async (reed, data) => {
      try {
        await reedsService.storeReed(reed);
        serverConnection.sendDataAck(data.id);
      } catch (error) {
        console.warn('ServerConnection: invalid mention reed signature, rejecting:', reed.id, error);
        serverConnection.sendDataInvalid(data.id);
        return;
      }

      const kept = await verifyAndStoreMention({ reedID: reed.id, authorID: reed.userID, createdAt: new Date().toISOString() });
      if (kept) {
        removeBroadcastReed(reed.id);
        await recordActivity(reed);
        dispatchReedToQueue(reed, 'mention');
        markUnread('mentions');
      }
    });
    serverConnection.onEncryptedReed(ServerEvent.BroadcastReed, 'broadcast reed', async (reed, data) => {
      // Ephemeral: never stored, no event id to ack/reject against.
      if (isBlankEcho(reed)) return;
      // Followed authors belong in the follow feed only — ignore if we follow them.
      if (reed.userID && (await followingRepository.isFollowing(reed.userID))) {
        return;
      }
      dispatchReedToQueue(reed, 'broadcast_reed', data.username);
    });
    serverConnection.on(ServerEvent.NewRipple, () => {
      markUnread('ripples');
    });
    serverConnection.on(ServerEvent.NewVouch, async ({ vouchId: vouchID }) => {
      // Only the id is pushed, so fetch and verify the cert rather than
      // trusting a payload the server assembled.
      await ingestPushedVouch(vouchID);
    });
    serverConnection.on(ServerEvent.PeerServerLost, ({ serverName }) => {
      notificationStore.warning(
        `Lost connection to ${serverName}. Live updates from it have stopped.`
      );
    });
    serverConnection.on(ServerEvent.ReedRemoved, async (data) => {
      const eventId = data.id;
      const cert = data.cert;
      if (!cert) {
        console.warn('ServerConnection: reed removal without a cert');
        if (eventId) serverConnection.sendDataInvalid(eventId);
        return;
      }
      if (await verifyAndCommitReedRemoval(cert)) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: reed removal cert failed verification:', cert.reedId);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.AccountRemoved, async (data) => {
      const eventId = data.id;
      const cert = data.cert;
      if (!cert) {
        console.warn('ServerConnection: account removal without a cert');
        if (eventId) serverConnection.sendDataInvalid(eventId);
        return;
      }
      if (await verifyAndCommitAccountRemoval(cert)) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: account removal cert failed verification:', cert.userId);
        serverConnection.sendDataInvalid(eventId);
      }
    });
    serverConnection.on(ServerEvent.UserBlocked, async (data) => {
      if (data.block) await receiveBlock(data.block);
    });
    serverConnection.on(ServerEvent.UserUnblocked, async (data) => {
      if (data.userId) await receiveUnblock(data.userId);
    });
    setBlockedReporter((cert) => void receiveBlock(cert));
    serverConnection.on(ServerEvent.KeyRevoked, async (data) => {
      const eventId = data.id;
      if (data.revocation && (await applyKeyRevocation(data.revocation))) {
        serverConnection.sendDataAck(eventId);
      } else {
        console.warn('ServerConnection: key revocation failed verification:', data.revocation?.id);
        if (eventId) serverConnection.sendDataInvalid(eventId);
      }
    });
    // MailboxBell is hidden and unused for now — handler disabled so we
    // don't ack/store mailbox messages nothing reads. The server keeps
    // redelivering on catch-up, same as any other undelivered message.

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
      pendingVouchesRepository.syncPending();
      void syncPendingBlocks();
        syncPendingBackupEvents();
        void syncPendingEvictions();
      }
    }
    })();
    }
  }

  onMount(() => {
    if (serverKeyTrusted) startTrustedSession();
    return () => {
      stopReconnect?.();
    };
  });
</script>

{#if !serverKeyTrusted || $serverKeyRejected}
  <ServerKeyGate
    rejected={$serverKeyRejected}
    compromisedReason={$serverKeyCompromise?.reason ?? null}
    on:trusted={() => {
      serverKeyTrusted = true;
      startTrustedSession();
    }}
  />
{:else}
<div class="app-shell">
  <UpdateAvailableIndicator />

  <!-- The splash at / shows the logo itself, large, and nothing else. -->
  {#if $page.url.pathname !== '/'}
    <header>
      <h1><a href={headerLink}>💫 Syrinx</a></h1>
      <!-- MailboxBell hidden — unused for now, see the disabled Mailbox WS
           handler and boot-time refresh below. -->
    </header>
  {/if}

  <ServerIdMismatchIndicator />
  <ServerUnreachableIndicator />
  <OfflineIndicator />
  <slot />

  {#if $page.url.pathname !== '/'}
    {#if user}
      <ActivitySidebar />
    {/if}

    <BottomToolbar />
  {/if}

  <Notifications />
</div>
{/if}
