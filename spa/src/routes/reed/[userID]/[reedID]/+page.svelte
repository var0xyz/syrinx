<script>
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { reedsService, stripMarkdown, unsignedReedsProcessed } from '$lib/repositories/reeds';
  import { formatAbsoluteDateTime } from '$lib/utils/time';
  import { apiService } from '$lib/services/api';
  import { removeReedAsAuthor, verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
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
  import ReedStatsSubscription from '$lib/components/ReedStatsSubscription.svelte';
  import ConversationSection from '$lib/components/ConversationSection.svelte';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import ReedStatsInfoModal from '$lib/components/ReedStatsInfoModal.svelte';
  import { followReedQueue } from '$lib/repositories/reeds';
  import { parseReedRef, resolveThreadId } from '$lib/utils/reedRef';

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
  /** @type {'loading' | 'loaded' | 'failed'} */
  let statsStatus = 'loading';
  let statsTimeoutId = 0;
  const STATS_TIMEOUT_MS = 10_000;
  let loadingReed = !data.fromCache && !data.errorMessage;
  let fetchingReed = false;
  let reedNotFound = false;
  /** When set, show tombstone stub instead of full reed body. */
  let removedReedCert = null;
  /** When set, author deleted their account — tombstone + replies still shown. */
  let removedAccountCert = null;
  /** Drops stale async loadReed completions (overlapping reactive calls). */
  let loadSeq = 0;

  // Action buttons state
  let likesCount = 0;
  let isLiked = false;
  let isReplyModalOpen = false;
  let isEchoModalOpen = false;
  let isStatsInfoModalOpen = false;
  let conversationRefresh = 0;
  /** @type {import('$lib/components/ConversationSection.svelte').default | null} */
  let conversationSection = null;
  let lastHandledFollowReedId = '';

  $: parentThreadId = reed && reedMatchesRoute
    ? (reed.threadId || resolveThreadId(reed, reed.serverSignature?.serverID || localStorage.getItem('serverId') || ''))
    : '';

  $: followArrived = $followReedQueue?.reed;
  $: if (followArrived && followArrived.id !== lastHandledFollowReedId && (reedMatchesRoute || removedReedCert || removedAccountCert)) {
    lastHandledFollowReedId = followArrived.id;
    void onFollowReedArrived(followArrived);
  }

  $: userID = $page.params.userID;
  $: reedID = $page.params.reedID;
  $: isPending = !!(reed && !reed.serverSignature);
  // Params can update before `data` on same-route navigations; never show a
  // reed that doesn't match the URL (e.g. parent body under the new author).
  $: reedMatchesRoute = !!(reed && reed.id === reedID && reed.userID === userID);

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
    removedReedCert = next.removedReedCert ?? null;
    removedAccountCert = next.removedAccountCert ?? null;
    fetchingReed = false;
    resetStatsState();
    lastHandledFollowReedId = '';
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
    if (msg?.userID === userID && msg?.reedID === reedID) {
      clearStatsTimeout();
      statsStatus = 'loaded';
      echoCount = msg.echoes ?? echoCount;
      coveragePercent = msg.coveragePercent ?? coveragePercent;
      if (typeof msg.replies === 'number') {
        replyCount = msg.replies;
      }
    }
  }

  function handleReedEchoes(msg) {
    if (msg?.userID === userID && msg?.reedID === reedID) {
      if (typeof msg.echoes === 'number') {
        echoCount = msg.echoes;
      }
    }
  }

  function handleReedReplies(msg) {
    if (msg?.userID === userID && msg?.reedID === reedID) {
      if (typeof msg.replies === 'number') {
        replyCount = msg.replies;
      }
    }
  }

  function handleReedCoverage(msg) {
    if (msg?.userID === userID && msg?.reedID === reedID) {
      coveragePercent = msg.coveragePercent ?? coveragePercent;
    }
  }

  async function onFollowReedArrived(incoming) {
    const parentRef = parseReedRef(incoming.replying);
    if (parentRef?.authorId === userID && parentRef?.reedId === reedID) {
      if (statsStatus === 'loaded') replyCount += 1;
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
  });

  onDestroy(() => {
    clearStatsTimeout();
    serverConnection.off(ServerEvent.ReedStats, handleReedStats);
    serverConnection.off(ServerEvent.ReedEchoes, handleReedEchoes);
    serverConnection.off(ServerEvent.ReedReplies, handleReedReplies);
    serverConnection.off(ServerEvent.ReedCoverage, handleReedCoverage);
  });

  async function reloadFromCache() {
    if (!user || !userID || !reedID) return;
    const requestedUserID = userID;
    const requestedReedID = reedID;
    let found = await reedsService.getReed(requestedUserID, requestedReedID);
    if (!found && user.id === requestedUserID) {
      const pending = await reedsService.getUnsignedReed(requestedReedID);
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
    if (reed && reed.id === reedID) return;
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
        const result = await apiService.getReedOrRemoval(userID, reedID);
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
        await reedsService.discardUnsignedReed(reedID);
      } else {
        await removeReedAsAuthor(userID, reedID);
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
  function handleEcho() {
    if (isPending) return;
    isEchoModalOpen = true;
  }

  function handleReply() {
    if (isPending) return;
    isReplyModalOpen = true;
  }

  function handleStatsInfo() {
    isStatsInfoModalOpen = true;
  }

  async function handleShare() {
    if (!reed || isPending) return;

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

  function handleLike() {
    isLiked = !isLiked;
    if (isLiked) {
      likesCount += 1;
    } else {
      likesCount = Math.max(0, likesCount - 1);
    }
  }

</script>

  <Auth>
    <div class="reed-detail-container">
      {#key `${userID}/${reedID}`}
        {#if (reedMatchesRoute && reed?.serverSignature) || removedReedCert || removedAccountCert}
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
                  <Avatar userID={userID} username={authorUser?.username ?? userID} size="69px" />
                </a>
                <div class="author-info">
                  <a href="/profile/{userID}" class="author-name">{authorUser?.username ?? userID}</a>
                  <p class="reed-stats" title="Stats for nerds">
                    {#if statsStatus === 'loading'}
                      Loading stats...
                    {:else if statsStatus === 'failed'}
                      Failed to load stats
                    {:else}
                      <span class="reed-stat-icon echoes" aria-hidden="true"></span>
                      {echoCount}
                      <span class="reed-stat-icon replies" aria-hidden="true"></span>
                      {replyCount}
                      <span class="reed-stat-icon coverage" aria-hidden="true"></span>
                      {coveragePercent}%
                    {/if}
                  </p>
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
          <ConversationSection
            bind:this={conversationSection}
            parentUserID={userID}
            parentReedID={reedID}
            threadId={parentThreadId}
            refreshToken={conversationRefresh}
          />
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
                  <a href="/profile/{reed.userID}" class="author-name">{authorUser?.username ?? reed.userID}</a>
                  <p class="reed-date" class:pending={isPending}>{isPending ? 'Pending…' : formatAbsoluteDateTime(reed.serverSignature?.timestamp)}</p>
                  {#if !isPending}
                    <button type="button" class="reed-stats" on:click={handleStatsInfo} aria-label="Reed stats — click for details">
                      {#if statsStatus === 'loading'}
                        Loading stats...
                      {:else if statsStatus === 'failed'}
                        Failed to load stats
                      {:else}
                        <span class="reed-stat-icon echoes" aria-hidden="true"></span>
                        {echoCount}
                        <span class="reed-stat-icon replies" aria-hidden="true"></span>
                        {replyCount}
                        <span class="reed-stat-icon coverage" aria-hidden="true"></span>
                        {coveragePercent}%
                        <span class="reed-stat-icon info" aria-hidden="true"></span>
                      {/if}
                    </button>
                  {/if}
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
              <button class="action-btn" on:click={handleEcho} aria-label="Echo" disabled={isPending}>
                <span class="action-icon icon-echo"></span>
                <span class="action-label">Echo</span>
              </button>
              <button class="action-btn" on:click={handleReply} aria-label="Reply" disabled={isPending}>
                <span class="action-icon icon-reply"></span>
                <span class="action-label">Reply</span>
              </button>
              <button class="action-btn" on:click={handleShare} aria-label="Share" disabled={isPending}>
                <span class="action-icon icon-share"></span>
                <span class="action-label">Share</span>
              </button>
              <!--
              <button
                class="action-btn like-btn"
                class:liked={isLiked}
                on:click={handleLike}
                aria-label="Like"
              >
                <span class="action-icon">{isLiked ? '❤️' : '🤍'}</span>
                <span class="action-label">
                  {likesCount > 0 ? likesCount : 'Like'}
                </span>
              </button>
              -->
            </div>
          </div>
          {#if !isPending}
            <ConversationSection
              bind:this={conversationSection}
              parentUserID={userID}
              parentReedID={reedID}
              threadId={parentThreadId}
              refreshToken={conversationRefresh}
            />
          {/if}
        {/if}
      </div>

      <!-- Bottom Toolbar -->
      <BottomToolbar currentPage="reeds" />
    </div>

    <NewReedModal open={isReplyModalOpen} replyingTo={reed} on:close={() => { isReplyModalOpen = false; }} />
    <NewReedModal open={isEchoModalOpen} echoOf={reed} on:close={() => { isEchoModalOpen = false; }} />
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

  .author-name {
    display: block;
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
    text-decoration: none;
    word-break: break-word;
    margin-bottom: 0.25rem;
  }

  .author-name:hover {
    text-decoration: underline;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .reed-stats {
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

  .quote-container {
    margin: 1rem 0;
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
  .icon-share {
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
