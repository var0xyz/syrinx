<script>
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { reedsService, stripMarkdown, unsignedReedsProcessed } from '$lib/repositories/reeds';
  import { formatAbsoluteDateTime } from '$lib/utils/time';
  import { apiService } from '$lib/services/api';
  import { removeReedAsAuthor, verifyAndCommitReedRemoval, reedRemovalCommitted, reedRemovalCommittedID } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval, accountRemovalCommitted } from '$lib/services/accountRemoval';
  import { removedReedsRepository } from '$lib/repositories/removedReeds';
  import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
  import { likeReed, unlikeReed, isReedLiked } from '$lib/services/reedLike';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { userRepository } from '$lib/repositories/user';
  import { goto } from '$app/navigation';
  import { notificationStore } from '$lib/stores/notifications';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { isOnline } from '$lib/services/pwa';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import ReedStatsSubscription from '$lib/components/ReedStatsSubscription.svelte';
  import ConversationSection from '$lib/components/ConversationSection.svelte';
  import RipplesSection from '$lib/components/RipplesSection.svelte';
  import ChorusSection from '$lib/components/ChorusSection.svelte';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import ReedStatsInfoModal from '$lib/components/ReedStatsInfoModal.svelte';
  import { followReedQueue, reedReplyQueue } from '$lib/repositories/reeds';
  import { resolveThreadId, refForRemoved, refForReed } from '$lib/utils/reedRef';
  import { isBlankEcho, resolveBlankEchoChain } from '$lib/utils/emptyEcho';

  /** @type {import('./$types').PageData} */
  export let data;

  let user = data.user;
  let authorUser = data.authorUser;
  let reed = data.reed;
  let echoedReed = data.echoedReed;
  let echoedReedMissing = data.echoedReedMissing;
  let repliedToReed = data.repliedToReed;
  let repliedToReedMissing = data.repliedToReedMissing;
  let errorMessage = data.errorMessage;
  let echoCount = 0;
  let replyCount = 0;
  let coveragePercent = 0;
  let likeCount = 0;
  /** @type {'loading' | 'loaded' | 'failed'} */
  let statsStatus = 'loading';
  let statsTimeoutId = 0;
  const STATS_TIMEOUT_MS = 10_000;
  let loadingReed = !data.fromCache && !data.errorMessage;
  let fetchingReed = false;
  let reedNotFound = false;
  /** Reed exists in local cache, but the server 404s it (e.g. its home
   * server was disconnected from the mesh) — conversation stays visible,
   * but nothing else about the reed can be trusted or interacted with. */
  let reedNotRecognized = false;
  /** When set, show tombstone stub instead of full reed body. */
  let removedReedCert = null;
  /** When set, author deleted their account — tombstone + replies still shown. */
  let removedAccountCert = null;
  /** Drops stale async loadReed completions (overlapping reactive calls). */
  let loadSeq = 0;

  // Action buttons state
  let isLiked = false;
  let isReplyModalOpen = false;
  let isEchoModalOpen = false;
  /** Target for the reply/echo modals — resolved off `reed` through any
   * blank-echo chain so replying/echoing a blank echo always lands on the
   * real original instead. */
  let replyEchoTarget = reed;
  let isStatsInfoModalOpen = false;
  /** @type {import('$lib/components/ConversationSection.svelte').default | null} */
  let conversationSection = null;
  let lastHandledFollowReedId = '';
  let lastHandledReedReplyId = '';

  // Which discussion tab is active lives in the URL hash (#ripples), not
  // plain component state — same reasoning as the follow-list modal on
  // the profile page: a reload or a shared link should land back on the
  // tab the user was looking at instead of always resetting to
  // Conversation. Absence of the hash (or any other value) means the
  // default, Conversation.
  /** @param {string} hash @returns {'conversation' | 'ripples' | 'chorus'} */
  function discussionTabFromHash(hash) {
    const normalized = (hash || '').replace(/^#/, '').toLowerCase();
    if (normalized === 'ripples') return 'ripples';
    if (normalized === 'chorus') return 'chorus';
    return 'conversation';
  }

  $: discussionTab = discussionTabFromHash($page.url.hash);

  /** @param {'conversation' | 'ripples' | 'chorus'} tab */
  function setDiscussionTab(tab) {
    const hash = tab === 'conversation' ? '' : `#${tab}`;
    void goto(`/reed/${userID}/${reedID}${hash}`, {
      replaceState: true,
      noScroll: true,
      keepFocus: true,
    });
  }

  let conversationCount = 0;
  let ripplesCount = 0;
  let chorusCount = 0;

  // A removed reed's own body (and thus any threadId it inherited) is
  // gone — self-reference using the removal cert's serverID so replies
  // cached from before the removal still key correctly, same as a live
  // thread-root reed would.
  $: parentThreadId = reed && reedMatchesRoute
    ? (reed.threadId || resolveThreadId(reed))
    : (removedReedCert || removedAccountCert)
      ? refForRemoved(
          userID,
          (removedReedCert || removedAccountCert).serverID || localStorage.getItem('serverId') || '',
          reedID,
        )
      : '';

  // Deleted accounts leave no username behind (the local tombstone stub
  // carries no `username` field) — show the raw identity instead of a
  // name that no longer means anything.
  $: authorDisplayName = removedAccountCert
    ? (removedAccountCert.serverID ? `~${userID}@${removedAccountCert.serverID}` : `~${userID}`)
    : (authorUser?.username ?? userID);

  $: followArrived = $followReedQueue?.reed;
  $: if (followArrived && followArrived.id !== lastHandledFollowReedId && (reedMatchesRoute || removedReedCert || removedAccountCert)) {
    lastHandledFollowReedId = followArrived.id;
    void onFollowReedArrived(followArrived);
  }

  // Reply from an author we don't necessarily follow, pushed because we're
  // subscribed to this reed's stats — see REED_REPLY in +layout.svelte.
  $: reedReplyArrived = $reedReplyQueue?.reed;
  $: if (reedReplyArrived && reedReplyArrived.id !== lastHandledReedReplyId && (reedMatchesRoute || removedReedCert || removedAccountCert)) {
    lastHandledReedReplyId = reedReplyArrived.id;
    void onFollowReedArrived(reedReplyArrived);
  }

  $: userID = $page.params.userID;
  $: reedID = $page.params.reedID;
  // The single canonical id (authorID@serverID/uuid) every reed-scoped API
  // call/subscription below this point should use. reed.id is already
  // canonical (see types/reed.ts), so no further composition is needed.
  $: canonicalReedID = reed ? reed.id : (userID && reedID ? refForReed(userID, reedID) : null);
  $: isPending = !!(reed && !reed.serverSignature);
  // Blank echoes (a bare re-share with no commentary) carry no
  // interactions of their own — no stats, no conversation/ripples, no live
  // updates. The interactions bar shows disabled rather than being hidden,
  // so the layout doesn't jump.
  $: isBlankEchoView = isBlankEcho(reed);
  // Params can update before `data` on same-route navigations; never show a
  // reed that doesn't match the URL (e.g. parent body under the new author).
  $: reedMatchesRoute = !!(reed && reed.id === refForReed(userID, reedID));

  // Apply fresh load() results when navigating between reeds.
  $: applyPageData(data);

  function applyPageData(next) {
    user = next.user;
    authorUser = next.authorUser;
    reed = next.reed;
    echoedReed = next.echoedReed;
    echoedReedMissing = next.echoedReedMissing;
    repliedToReed = next.repliedToReed;
    repliedToReedMissing = next.repliedToReedMissing;
    errorMessage = next.errorMessage;
    reedNotFound = false;
    reedNotRecognized = false;
    removedReedCert = next.removedReedCert ?? null;
    removedAccountCert = next.removedAccountCert ?? null;
    fetchingReed = false;
    resetStatsState();
    lastHandledFollowReedId = '';
    lastHandledReedReplyId = '';
    isLiked = false;
    if (next.reed?.userID && next.reed?.id) {
      void isReedLiked(next.reed.userID, next.reed.id).then((liked) => {
        if (reed?.id === next.reed.id) isLiked = liked;
      });
    }
    loadingReed = !next.fromCache && !next.errorMessage;
    if (next.fromCache) {
      void afterCacheHit(next);
    } else if (!next.errorMessage) {
      void loadReedFromNetwork();
    }
  }

  async function afterCacheHit(next) {
    if (next.reed?.userID === next.user?.id && !next.reed.serverSignature) {
      void reedsService.publishUnsignedReed(next.reed);
    }
    if (!authorUser) {
      await loadAuthorProfile();
    }
    if (next.reed?.serverSignature) {
      void checkServerRecognizesReed(next.reed.id);
    }
  }

  /**
   * A reed can be cached locally (e.g. from a server later disconnected
   * from the mesh) while the server itself now 404s it. loadReedFromNetwork
   * never runs for a cache hit, so this is the only place that re-checks —
   * fire-and-forget, best-effort, and dropped if the user has since
   * navigated to a different reed.
   */
  async function checkServerRecognizesReed(reedRef) {
    if (!$isOnline) return;
    try {
      await serverConnection.connect();
      if (canonicalReedID !== reedRef) return;
      const result = await apiService.getReedOrRemoval(reedRef);
      if (canonicalReedID !== reedRef) return;
      if (result.kind === 'not_found') {
        reedNotRecognized = true;
      }
    } catch {
      // Network/relay hiccup — not conclusive, so don't flag the reed.
    }
  }

  function clearStatsTimeout() {
    if (statsTimeoutId) {
      clearTimeout(statsTimeoutId);
      statsTimeoutId = 0;
    }
  }

  function resetStatsState() {
    echoCount = 0;
    replyCount = 0;
    coveragePercent = 0;
    likeCount = 0;
    statsStatus = 'loading';
    clearStatsTimeout();
  }

  function armStatsTimeout() {
    clearStatsTimeout();
    statsTimeoutId = window.setTimeout(() => {
      statsTimeoutId = 0;
      if (statsStatus === 'loading') statsStatus = 'failed';
    }, STATS_TIMEOUT_MS);
  }

  function onStatsSubscribeOk() {
    if (statsStatus === 'loading') armStatsTimeout();
  }

  function onStatsSubscribeFailed() {
    clearStatsTimeout();
    if (statsStatus === 'loading') statsStatus = 'failed';
  }

  function handleReedStats(msg) {
    if (msg?.reedID === refForReed(userID, reedID)) {
      clearStatsTimeout();
      statsStatus = 'loaded';
      echoCount = msg.echoes ?? echoCount;
      coveragePercent = msg.coveragePercent ?? coveragePercent;
      if (typeof msg.replies === 'number') {
        replyCount = msg.replies;
      }
      if (typeof msg.likes === 'number') {
        likeCount = msg.likes;
      }
    }
  }

  function handleReedEchoes(msg) {
    if (msg?.reedID === refForReed(userID, reedID)) {
      if (typeof msg.echoes === 'number') {
        echoCount = msg.echoes;
      }
    }
  }

  function handleReedReplies(msg) {
    if (msg?.reedID === refForReed(userID, reedID)) {
      if (typeof msg.replies === 'number') {
        replyCount = msg.replies;
      }
    }
  }

  function handleReedLikes(msg) {
    if (msg?.reedID === refForReed(userID, reedID)) {
      if (typeof msg.likes === 'number') {
        likeCount = msg.likes;
      }
    }
  }

  /**
   * Fires after ANY reed removal cert is committed locally (see
   * reedRemoval.ts) — could be a reply anywhere in this thread, not just
   * the reed this page itself is subscribed to. Splices the row out in
   * place instead of reloading the whole conversation, which would reset
   * the viewer's scroll position. A miss for an unrelated removal is a
   * harmless no-op filter.
   */
  $: if ($reedRemovalCommittedID) conversationSection?.onReplyRemoved($reedRemovalCommittedID);

  /**
   * A removal cert (reed or account) can arrive over WS while this exact
   * reed is on screen — e.g. the author deletes it live. Re-check whether
   * it's *this* reed/author so the page tombstones immediately instead of
   * continuing to show stale content and stay subscribed to stats/coverage
   * for a reed the server no longer considers to exist.
   */
  $: if ($reedRemovalCommitted > 0 || $accountRemovalCommitted > 0) {
    void checkLiveRemoval();
  }

  async function checkLiveRemoval() {
    if (!reedMatchesRoute || removedReedCert || removedAccountCert) return;
    const accountCert = await removedAccountsRepository.get(userID);
    if (accountCert) {
      removedAccountCert = accountCert;
      removedReedCert = null;
      reed = null;
      return;
    }
    const reedCert = await removedReedsRepository.get(refForReed(userID, reedID));
    if (reedCert && reedCert.userID === userID) {
      removedReedCert = reedCert;
      reed = null;
    }
  }

  function handleReedCoverage(msg) {
    if (msg?.reedID === refForReed(userID, reedID)) {
      coveragePercent = msg.coveragePercent ?? coveragePercent;
    }
  }

  // replyCount is set only from the authoritative REED_STATS/REED_REPLIES
  // push (handleReedStats/handleReedReplies below) — no local increment
  // here, since that push already lands on the same fanout wave and an
  // extra += 1 on top double-counts the very reply it's confirming.
  async function onFollowReedArrived(incoming) {
    if (incoming?.replying && incoming.replying === canonicalReedID) {
      await conversationSection?.onReplyArrived(incoming);
    }
  }

  $: if ($isOnline && user && loadingReed && !data.fromCache) loadReedFromNetwork();
  $: if ($unsignedReedsProcessed > 0 && user) reloadFromCache();

  onMount(() => {
    serverConnection.on(ServerEvent.ReedStats, handleReedStats);
    serverConnection.on(ServerEvent.ReedEchoes, handleReedEchoes);
    serverConnection.on(ServerEvent.ReedReplies, handleReedReplies);
    serverConnection.on(ServerEvent.ReedCoverage, handleReedCoverage);
    serverConnection.on(ServerEvent.ReedLikes, handleReedLikes);
  });

  onDestroy(() => {
    clearStatsTimeout();
    serverConnection.off(ServerEvent.ReedStats, handleReedStats);
    serverConnection.off(ServerEvent.ReedEchoes, handleReedEchoes);
    serverConnection.off(ServerEvent.ReedReplies, handleReedReplies);
    serverConnection.off(ServerEvent.ReedCoverage, handleReedCoverage);
    serverConnection.off(ServerEvent.ReedLikes, handleReedLikes);
  });

  async function reloadFromCache() {
    if (!user || !userID || !reedID) return;
    const requestedUserID = userID;
    const requestedReedID = reedID;
    const requestedCanonicalReedID = refForReed(requestedUserID, requestedReedID);
    let found = await reedsService.getReed(requestedCanonicalReedID);
    if (!found && user.id === requestedUserID) {
      const pending = await reedsService.getUnsignedReed(requestedCanonicalReedID);
      if (pending?.userID === requestedUserID) found = pending;
    }
    // Drop stale completions after navigating to another reed.
    if (requestedUserID !== userID || requestedReedID !== reedID) return;
    if (found) {
      reed = found;
      loadingReed = false;
      await afterCacheHit({
        user,
        userID: requestedUserID,
        reedID: requestedReedID,
        reed: found,
        authorUser,
        fromCache: true,
      });
    }
  }

  async function loadReedFromNetwork() {
    if (!user || !userID || !reedID) return;
    if (reed && reed.id === refForReed(userID, reedID)) return;
    if (!$isOnline) {
      loadingReed = false;
      return;
    }

    const seq = ++loadSeq;
    try {
      loadingReed = true;
      errorMessage = '';
      reedNotFound = false;

      await serverConnection.connect();
      if (seq !== loadSeq) return;
      if (!serverConnection.isConnected()) {
        return;
      }

      try {
        const result = await apiService.getReedOrRemoval(refForReed(userID, reedID));
        if (seq !== loadSeq) return;
        if (result.kind === 'not_found') {
          reedNotFound = true;
          return;
        }
        if (result.kind === 'gone') {
          if (result.removal.type === 'reed') {
            await verifyAndCommitReedRemoval(result.removal);
            removedReedCert = result.removal;
            loadingReed = false;
            await loadAuthorProfile();
          } else if (result.removal.type === 'account') {
            await verifyAndCommitAccountRemoval(result.removal);
            removedAccountCert = result.removal;
            removedReedCert = null;
            loadingReed = false;
            await loadAuthorProfile();
          } else {
            reedNotFound = true;
          }
          return;
        }
        // result.kind === 'reed' confirms the reed exists (and isn't
        // removed) but carries only tip metadata, not full signed
        // content -- the server never stores reed bodies (see db.go's
        // "server never stores reed content" comment). Fall through to
        // requestReedContent below, which relays the real, fully-signed
        // body from a peer that holds it.
      } catch {
        if (seq !== loadSeq) return;
        reedNotFound = true;
        return;
      }

      if (seq !== loadSeq) return;

      loadingReed = false;
      fetchingReed = true;
      try {
        const networkReed = await serverConnection.requestReedContent(reedID, userID, userID);
        if (seq !== loadSeq) return;
        reed = networkReed;
        await loadAuthorProfile();
      } catch {
        if (seq !== loadSeq) return;
        reedNotFound = true;
      } finally {
        if (seq === loadSeq) fetchingReed = false;
      }
    } catch (error) {
      console.error('Error loading reed:', error);
      if (seq === loadSeq) errorMessage = 'Failed to load reed';
    } finally {
      if (seq === loadSeq) loadingReed = false;
    }
  }

  async function loadAuthorProfile() {
    // Prefer local cache — never block the detail view on a network profile fetch.
    authorUser = await userRepository.get(userID).catch(() => null);
    if (authorUser || !$isOnline) return;
    try {
      const fresh = await apiService.getUserProfile(userID);
      await userRepository.put(fresh);
      authorUser = fresh;
    } catch (error) {
      console.error('Failed to fetch author profile:', error);
    }
  }

  async function deleteReed() {
    if (!confirm('Are you sure you want to delete this reed?')) {
      return;
    }

    await performDelete();
  }

  async function performDelete() {
    try {
      if (reed && !reed.serverSignature) {
        await reedsService.discardUnsignedReed(reed.id);
      } else {
        await removeReedAsAuthor(canonicalReedID);
      }
      goto('/reeds');
    } catch (error) {
      console.error('Error deleting reed:', error);
      errorMessage = 'Failed to delete reed';
    }
  }

  /** Prefer history.back so list pages can restore scroll via snapshots. */
  function goBack() {
    if (typeof history !== 'undefined' && history.length > 1) {
      history.back();
      return;
    }
    goto('/reeds');
  }

  // Action button handlers
  async function resolveReplyEchoTarget() {
    if (!reed) return reed;
    return resolveBlankEchoChain(reed, (canonicalRef) => reedsService.getReed(canonicalRef));
  }

  async function handleEcho() {
    if (isPending || isBlankEchoView || reedNotRecognized) return;
    replyEchoTarget = await resolveReplyEchoTarget();
    isEchoModalOpen = true;
  }

  async function handleReply() {
    if (isPending || isBlankEchoView || reedNotRecognized) return;
    replyEchoTarget = await resolveReplyEchoTarget();
    isReplyModalOpen = true;
  }

  function handleStatsInfo() {
    if (isBlankEchoView) return;
    isStatsInfoModalOpen = true;
  }

  async function handleShare() {
    if (!reed || isPending || isBlankEchoView || reedNotRecognized) return;

    const reedUrl = `${window.location.origin}/reed/${userID}/${reedID}`;
    const reedText = stripMarkdown(reed.content);
    const shareData = {
      title: `${authorUser?.username ?? userID}'s Reed`,
      text: reedText,
      url: reedUrl
    };

    // Check if Web Share API is available
    if (navigator.share) {
      try {
        await navigator.share(shareData);
      } catch (error) {
        // User cancelled or error occurred
        if (error.name !== 'AbortError') {
          console.error('Error sharing:', error);
          notificationStore.error('Failed to share reed');
        }
      }
    } else {
      // Fallback: copy URL to clipboard
      try {
        await navigator.clipboard.writeText(reedUrl);
        notificationStore.success('Reed URL copied to clipboard');
      } catch (error) {
        console.error('Error copying to clipboard:', error);
        notificationStore.error('Failed to copy reed URL');
      }
    }
  }

  async function handleLike() {
    if (isPending || isBlankEchoView || reedNotRecognized) return;
    const wasLiked = isLiked;
    isLiked = !wasLiked;
    try {
      if (wasLiked) {
        await unlikeReed(userID, reedID);
      } else {
        await likeReed(userID, reedID);
      }
    } catch (error) {
      console.error('Error toggling like:', error);
      // The like/unlike is queued (pendingLike/pendingUnlike) before the
      // network call runs, so a network failure alone doesn't mean the
      // action was lost — re-check the effective state (which overlays
      // pending actions) rather than blindly reverting the optimistic flip.
      isLiked = await isReedLiked(userID, reedID);
    }
  }

</script>

  <Auth>
    <div class="reed-detail-container">
      {#key `${userID}/${reedID}`}
        {#if !isBlankEchoView && reedMatchesRoute && reed?.serverSignature && !removedReedCert && !removedAccountCert}
          <ReedStatsSubscription
            authorId={userID}
            reedId={reedID}
            onSubscribeOk={onStatsSubscribeOk}
            onSubscribeFailed={onStatsSubscribeFailed}
          />
        {/if}
      {/key}

      <!-- Content -->
      <div class="reed-content">
        {#if fetchingReed}
          <div class="loading">
            <h2>Fetching reed...</h2>
            <p>Retrieving from the network.</p>
          </div>
        {:else if reedNotFound}
          <div class="error-state">
            <div class="error-icon">🪹</div>
            <h3>Reed not found</h3>
            <p>This reed doesn't exist or has been deleted.</p>
            <button class="btn btn-primary" on:click={goBack}>Go Back</button>
          </div>
        {:else if !$isOnline && loadingReed}
          <div class="error-state">
            <div class="error-icon">📡</div>
            <h3>You're offline</h3>
            <p>This reed isn't cached locally. We'll load it when you're back online.</p>
          </div>
        {:else if removedReedCert || removedAccountCert}
          <div class="reed-detail removed-reed">
            <div class="reed-meta">
              <div class="reed-author">
                <a href="/profile/{userID}" class="author-avatar">
                  <Avatar userID={userID} username={authorDisplayName} size="69px" />
                </a>
                <div class="author-info">
                  <Username
                    userID={userID}
                    username={authorDisplayName}
                    class="author-name"
                  />
                </div>
              </div>
            </div>
            <div class="reed-body tombstone">
              <p class="tombstone-text">
                {#if removedReedCert?.serverSignature?.timestamp}
                  On {formatAbsoluteDateTime(removedReedCert.serverSignature.timestamp)} the author removed this reed.
                {:else if removedReedCert}
                  The author removed this reed.
                {:else if removedAccountCert?.serverSignature?.timestamp}
                  On {formatAbsoluteDateTime(removedAccountCert.serverSignature.timestamp)} the author deleted their account.
                {:else}
                  The author deleted their account.
                {/if}
              </p>
            </div>
          </div>
          <!-- No discussion tabs: the reed can't be interacted with (no
               replying/echoing) and Ripples/Chorus lose meaning once nobody
               can see the original content. Replies made before removal are
               still real, though, so show them directly if any exist. -->
          <div class="discussion-panel" class:hidden={conversationCount === 0}>
            <ConversationSection
              bind:this={conversationSection}
              parentUserID={userID}
              parentReedID={reedID}
              threadId={parentThreadId}
              bind:count={conversationCount}
            />
          </div>
        {:else if loadingReed || !reedMatchesRoute}
          <div class="loading">
            <h2>Loading reed...</h2>
            <p>Please wait while we fetch the reed details.</p>
          </div>
        {:else if errorMessage}
          <div class="error-state">
            <div class="error-icon">⚠️</div>
            <h3>Error</h3>
            <p>{errorMessage}</p>
            <button class="btn btn-primary" on:click={goBack}>Go Back</button>
          </div>
        {:else if reed}
          <div class="reed-detail">
            <div class="reed-meta">
              <div class="reed-author">
                <a href="/profile/{reed.userID}" class="author-avatar">
                  <Avatar userID={reed.userID} username={authorUser?.username ?? reed.userID} size="69px" />
                </a>
                <div class="author-info">
                  <Username
                    userID={reed.userID}
                    username={authorUser?.username ?? reed.userID}
                    class="author-name"
                  />
                  <p class="reed-date">{isPending ? 'Pending…' : formatAbsoluteDateTime(reed.serverSignature?.timestamp)}</p>
                  <button
                    type="button"
                    class="reed-stats"
                    class:invisible={isBlankEchoView || isPending}
                    on:click={handleStatsInfo}
                    aria-label="Reed stats — click for details"
                    aria-hidden={isBlankEchoView}
                    tabindex={isBlankEchoView ? -1 : 0}
                  >
                    {#if isPending}
                      &nbsp;
                    {:else if statsStatus === 'loading'}
                      Loading stats...
                    {:else if statsStatus === 'failed'}
                      Failed to load stats
                    {:else}
                      <span class="reed-stat-icon replies" aria-hidden="true"></span>
                      {replyCount}
                      <span class="reed-stat-icon echoes" aria-hidden="true"></span>
                      {echoCount}
                      <span class="reed-stat-icon likes" aria-hidden="true"></span>
                      {likeCount}
                      <span class="reed-stat-icon coverage" aria-hidden="true"></span>
                      {coveragePercent}%
                      <span class="reed-stat-icon info" aria-hidden="true"></span>
                    {/if}
                  </button>
                </div>
              </div>
              {#if user?.id === reed.userID}
                <div class="reed-actions">
                  <KebabMenu options={[{ label: 'Delete', danger: true, icon: '/icons/trash-16.png', onSelect: deleteReed }]} />
                </div>
              {/if}
            </div>

            <div class="reed-body">
              {#if reed.replying}
                <div class="quote-container">
                  <Quote reed={repliedToReed} reedRef={reed.replying} missing={repliedToReedMissing} type="reply" linked={true} />
                </div>
              {/if}
              {#if reed.content}
                <MarkdownParser text={reed.content} />
              {/if}
              {#if reed.echoing}
                <div class="quote-container">
                  <Quote reed={echoedReed} reedRef={reed.echoing} missing={echoedReedMissing} type="echo" linked={true} />
                </div>
              {/if}
            </div>

            <div class="reed-actions-bar">
              <button class="action-btn" on:click={handleReply} aria-label="Reply" disabled={isPending || isBlankEchoView || reedNotRecognized}>
                <span class="action-icon icon-reply"></span>
                <span class="action-label">Reply</span>
              </button>
              <button class="action-btn" on:click={handleEcho} aria-label="Echo" disabled={isPending || isBlankEchoView || reedNotRecognized}>
                <span class="action-icon icon-echo"></span>
                <span class="action-label">Echo</span>
              </button>
              <button class="action-btn" on:click={handleLike} aria-label={isLiked ? 'Unlike' : 'Like'} disabled={isPending || isBlankEchoView || reedNotRecognized}>
                <span class="action-icon icon-like" class:filled={isLiked}></span>
                <span class="action-label">Like</span>
              </button>
              <button class="action-btn" on:click={handleShare} aria-label="Share" disabled={isPending || isBlankEchoView || reedNotRecognized}>
                <span class="action-icon icon-share"></span>
                <span class="action-label">Share</span>
              </button>
            </div>
          </div>
          {#if reedNotRecognized}
            <p class="not-recognized-notice">Reed not recognized by the server</p>
            <!-- Ripples/Chorus lose meaning once the server won't vouch for
                 the reed — same reasoning as the removed-reed branch above.
                 Conversation stays: replies cached before disconnection are
                 still real. -->
            <div class="discussion-panel" class:hidden={conversationCount === 0}>
              <ConversationSection
                bind:this={conversationSection}
                parentUserID={userID}
                parentReedID={reedID}
                threadId={parentThreadId}
                bind:count={conversationCount}
              />
            </div>
          {:else if !isPending && !isBlankEchoView}
            <div class="discussion-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                class="discussion-tab"
                class:active={discussionTab === 'conversation'}
                aria-selected={discussionTab === 'conversation'}
                on:click={() => setDiscussionTab('conversation')}
              >
                Conversation{#if conversationCount > 0}&nbsp;({conversationCount}){/if}
              </button>
              <button
                type="button"
                role="tab"
                class="discussion-tab"
                class:active={discussionTab === 'ripples'}
                aria-selected={discussionTab === 'ripples'}
                on:click={() => setDiscussionTab('ripples')}
              >
                Ripples{#if ripplesCount > 0}&nbsp;({ripplesCount}){/if}
              </button>
              <button
                type="button"
                role="tab"
                class="discussion-tab"
                class:active={discussionTab === 'chorus'}
                aria-selected={discussionTab === 'chorus'}
                on:click={() => setDiscussionTab('chorus')}
              >
                Chorus{#if chorusCount > 0}&nbsp;({chorusCount}){/if}
              </button>
            </div>
            <div class="discussion-panel" class:hidden={discussionTab !== 'conversation'}>
              <ConversationSection
                bind:this={conversationSection}
                parentUserID={userID}
                parentReedID={reedID}
                threadId={parentThreadId}
                bind:count={conversationCount}
              />
            </div>
            <div class="discussion-panel" class:hidden={discussionTab !== 'ripples'}>
              <RipplesSection reedID={canonicalReedID} serverSignatureArmor={reed.serverSignature?.armor ?? ''} bind:count={ripplesCount} />
            </div>
            <div class="discussion-panel" class:hidden={discussionTab !== 'chorus'}>
              <ChorusSection reedID={canonicalReedID} bind:count={chorusCount} />
            </div>
          {/if}
        {/if}
      </div>

      <!-- Bottom Toolbar -->
      <BottomToolbar currentPage="reeds" />
    </div>

    <NewReedModal open={isReplyModalOpen} replyingTo={replyEchoTarget} on:close={() => { isReplyModalOpen = false; }} />
    <NewReedModal open={isEchoModalOpen} echoOf={replyEchoTarget} on:close={() => { isEchoModalOpen = false; }} />
    <ReedStatsInfoModal open={isStatsInfoModalOpen} on:close={() => { isStatsInfoModalOpen = false; }} />
  </Auth>

<style>
  .reed-detail-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }


  .reed-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .reed-detail.removed-reed .tombstone {
    padding: 1rem;
    color: var(--muted);
  }

  .reed-detail {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
  }

  .reed-meta {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-author {
    display: flex;
    align-items: stretch;
    gap: 1rem;
    min-width: 0;
  }

  .author-avatar {
    width: 69px;
    height: 69px;
    border-radius: 8px;
    overflow: hidden;
    display: flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
    text-decoration: none;
  }

  .author-info {
    min-width: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
  }

  :global(.author-name) {
    display: block;
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
    text-decoration: none;
    word-break: break-word;
    margin-bottom: 0.25rem;
  }

  :global(.author-name .username:hover) {
    text-decoration: underline;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .reed-stats {
    min-height: 1rem;
    display: inline-flex;
    align-items: end;
    gap: 0.45rem;
    margin: 0.25rem 0 0;
    padding: 0;
    background: none;
    border: none;
    cursor: pointer;
    color: var(--muted);
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    letter-spacing: 0.02em;
    opacity: 0.8;
  }

  .reed-stats:hover {
    opacity: 1;
    color: var(--fg);
  }

  /* Blank echoes have no stats to show, but the button stays in the
     layout (just invisible) so the date above it doesn't shift. */
  .reed-stats.invisible {
    visibility: hidden;
    pointer-events: none;
  }

  .reed-stat-icon {
    display: inline-block;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .reed-stat-icon.echoes {
    -webkit-mask-image: url('/icons/megaphone-16.png');
    mask-image: url('/icons/megaphone-16.png');
  }

  .reed-stat-icon.replies {
    margin-left: 0.15rem;
    -webkit-mask-image: url('/icons/reply-16.png');
    mask-image: url('/icons/reply-16.png');
  }

  .reed-stat-icon.coverage {
    margin-left: 0.15rem;
    -webkit-mask-image: url('/icons/graph-16.png');
    mask-image: url('/icons/graph-16.png');
  }

  .reed-stat-icon.likes {
    margin-left: 0.15rem;
    -webkit-mask-image: url('/icons/like-16-outlined.png');
    mask-image: url('/icons/like-16-outlined.png');
  }

  .reed-stat-icon.info {
    margin-left: 0.25rem;
    -webkit-mask-image: url('/icons/info-16.png');
    mask-image: url('/icons/info-16.png');
  }

  .reed-actions {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-body {
    padding: 1rem;
    word-break: break-word;
  }

  .not-recognized-notice {
    margin: 0;
    padding: 1rem 1rem 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .quote-container {
    margin: 1rem 0;
  }

  .discussion-tabs {
    display: flex;
    gap: 1.25rem;
    padding: 0 0.75rem;
    margin-top: 1rem;
  }

  .discussion-tab {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    padding: 0.6rem 0.1rem;
    font: inherit;
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--muted);
    cursor: pointer;
  }

  .discussion-tab.active {
    color: var(--fg);
    border-bottom-color: var(--primary);
  }

  .discussion-tab:hover {
    color: var(--fg);
  }

  .discussion-panel.hidden {
    display: none;
  }

  .reed-actions-bar {
    display: flex;
    gap: 0.5rem;
    padding: 1rem 1.5rem;
    border-top: 1px solid var(--border);
    background: var(--surface);
  }

  .action-btn {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    padding: 0.25rem 0.5rem;
    background: transparent;
    transition: all 0.2s ease;
    color: var(--fg);
  }

  .action-btn:hover {
    background: var(--input-bg);
    border-color: var(--primary);
  }

  .action-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .action-btn:disabled:hover {
    background: transparent;
    border-color: transparent;
  }

  .action-icon {
    font-size: 1.2rem;
    line-height: 1;
  }

  .icon-echo,
  .icon-reply,
  .icon-share,
  .icon-like {
    display: inline-block;
    width: 1.2rem;
    height: 1.2rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .icon-echo {
    -webkit-mask-image: url('/icons/megaphone-24.png');
    mask-image: url('/icons/megaphone-24.png');
  }

  .icon-reply {
    -webkit-mask-image: url('/icons/reply-24.png');
    mask-image: url('/icons/reply-24.png');
  }

  .icon-share {
    -webkit-mask-image: url('/icons/share-24.png');
    mask-image: url('/icons/share-24.png');
  }

  .icon-like {
    -webkit-mask-image: url('/icons/like-24-outlined.png');
    mask-image: url('/icons/like-24-outlined.png');
  }

  .icon-like.filled {
    -webkit-mask-image: url('/icons/like-24-filled.png');
    mask-image: url('/icons/like-24-filled.png');
  }

  .action-label {
    font-size: 0.75rem;
    color: var(--muted);
    font-weight: 500;
  }

  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
  }

  .error-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .error-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .error-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .error-state p {
    margin: 0 0 1rem 0;
    font-size: 0.9rem;
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    transition: all 0.2s ease;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
  }

  /* Responsive Design */
  @media (max-width: 768px) {
    .reed-content {
      padding: 0.5rem;
    }

    .reed-meta {
      padding: 0.75rem;
    }

    .reed-body {
      padding: 0.75rem;
    }

    .reed-actions-bar {
      padding: 0.25rem 1rem;
      gap: 0.375rem;
    }

    .action-btn {
      padding: 0.5rem 0.25rem;
    }

    .action-icon {
      font-size: 1.1rem;
    }

    .action-label {
      font-size: 0.7rem;
    }

    .quote-container {
      margin: 0.5rem 0;
    }
  }
</style>
