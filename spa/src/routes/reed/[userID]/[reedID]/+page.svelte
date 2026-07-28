<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { reedsService, stripMarkdown, countMarkdownCharacters, unsignedReedsProcessed } from '$lib/repositories/reeds';
  import { formatAbsoluteDateTime } from '$lib/utils/time';
  import { Reed } from '$lib/types/reed';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
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
  import { serverConnection } from '$lib/services/serverConnection';
  import { isOnline } from '$lib/services/pwa';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { echoCountsRepository } from '$lib/repositories/echoCounts';
  import Avatar from '$lib/components/Avatar.svelte';

  let user = null;
  let authorUser = null;
  let loading = true;
  let reed = null;
  let loadingReed = true;
  let errorMessage = '';
  let fetchingReed = false;
  let reedNotFound = false;
  let echoedReed = null;
  let echoedReedMissing = false;
  let repliedToReed = null;
  let repliedToReedMissing = false;
  let echoCount = 0;
  /** Drops stale async loadReed completions (overlapping reactive calls). */
  let loadSeq = 0;

  // Action buttons state
  let likesCount = 0;
  let isLiked = false;
  let isReplyModalOpen = false;
  let isEchoModalOpen = false;

  $: userID = $page.params.userID;
  $: reedID = $page.params.reedID;
  $: isPending = !!(reed && !reed.serverSignature);

  $: if (reedID && user) loadReed();
  $: if ($isOnline && user) loadReed();
  $: if ($unsignedReedsProcessed > 0 && user) loadReed(true);

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();

      if (!user) {
        // Redirect to home page if no user
        window.location.href = '/';
        return;
      }
    } catch (error) {
      console.error('Error getting user:', error);
      // Redirect to home page on error
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });

  async function loadReed(force = false) {
    if (!user || !userID || !reedID) return;

    // Already showing this reed (published or pending) — only forced reloads
    // (e.g. unsignedReedsProcessed after countersign) should run again.
    if (!force && reed && reed.id === reedID) return;

    const seq = ++loadSeq;
    try {
      loadingReed = true;
      errorMessage = '';
      reedNotFound = false;

      let found = await reedsService.getReed(userID, reedID);

      // Author-only: pending local reed. Do not ask the server for existence —
      // just show Pending… and try to countersign.
      if (!found && user?.id === userID) {
        const pending = await reedsService.getUnsignedReed(reedID);
        if (pending && pending.userID === userID) {
          found = pending;
          void reedsService.publishUnsignedReed(pending);
        }
      }

      if (seq !== loadSeq) return;

      if (!found) {
        // Not in IndexedDB — try to fetch from network
        await serverConnection.connect();
        if (seq !== loadSeq) return;
        if (!serverConnection.isConnected()) {
          return; // stay in loading state; $isOnline reactive will retry
        }

        // REST existence check (also applies reed-removal 410 certs)
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
              reedNotFound = true;
            } else if (result.removal.type === 'account') {
              await verifyAndCommitAccountRemoval(result.removal);
              reedNotFound = true;
            } else {
              reedNotFound = true;
            }
            return;
          }
        } catch (err) {
          // on network error: stay in loading state, $isOnline reactive will retry
          return;
        }

        if (seq !== loadSeq) return;

        // Reed exists on server — request content via WS relay
        loadingReed = false;
        fetchingReed = true;
        try {
          const data = await serverConnection.requestReedContent(reedID, userID, userID);
          if (seq !== loadSeq) return;
          reed = data;
          await loadAuthorProfile();
        } catch {
          if (seq !== loadSeq) return;
          reedNotFound = true;
        } finally {
          if (seq === loadSeq) fetchingReed = false;
        }
        return;
      }

      reed = found;

      // Verify that the user matches
      if (reed.userID !== userID) {
        errorMessage = 'This reed does not belong to the specified user';
        return;
      }

      // Load echoed reed if this is an echo
      echoedReed = null;
      echoedReedMissing = false;
      if (reed.echoing) {
        const echoRef = parseReedRef(reed.echoing);
        if (echoRef) {
          try {
            echoedReed = await reedsService.getReed(echoRef.authorId, echoRef.reedId);
            if (!echoedReed) {
              const data = await apiService.getReed(echoRef.authorId, echoRef.reedId);
              echoedReed = data;
            }
            if (!echoedReed) echoedReedMissing = true;
          } catch {
            echoedReedMissing = true;
          }
        } else {
          echoedReedMissing = true;
        }
      }

      // Load replied-to reed if this is a reply
      repliedToReed = null;
      repliedToReedMissing = false;
      if (reed.replying) {
        const replyRef = parseReedRef(reed.replying);
        if (replyRef) {
          try {
            repliedToReed = await reedsService.getReed(replyRef.authorId, replyRef.reedId);
            if (!repliedToReed) repliedToReedMissing = true;
          } catch {
            repliedToReedMissing = true;
          }
        } else {
          repliedToReedMissing = true;
        }
      }

      if (reed.serverSignature) {
        echoCount = await echoCountsRepository.get(reedID);
        refreshEchoCountInBackground(userID, reedID);
      }

      // Fetch the reed author's profile (local cache first, then API).
      await loadAuthorProfile();
    } catch (error) {
      console.error('Error loading reed:', error);
      if (seq === loadSeq) errorMessage = 'Failed to load reed';
    } finally {
      if (seq === loadSeq) loadingReed = false;
    }
  }

  async function refreshEchoCountInBackground(authorId, id) {
    try {
      const next = await apiService.getReedEchoCount(authorId, id);
      await echoCountsRepository.put(id, next);
      if (id === reedID) echoCount = next;
    } catch (error) {
      console.warn('Background echo count refresh failed:', error);
    }
  }

  async function loadAuthorProfile() {
    authorUser = await userRepository.getByUserId(userID).catch(() => null);
    if (authorUser) return;
    try {
      const fresh = await apiService.getUser(userID);
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

  // Action button handlers
  function handleEcho() {
    if (isPending) return;
    isEchoModalOpen = true;
  }

  function handleReply() {
    if (isPending) return;
    isReplyModalOpen = true;
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

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading...</h2>
        <p>Please wait while we fetch the reed.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="reed-detail-container">

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
            <button class="btn btn-primary" on:click={() => goto('/reeds')}>Go Back</button>
          </div>
        {:else if !$isOnline && loadingReed}
          <div class="error-state">
            <div class="error-icon">📡</div>
            <h3>You're offline</h3>
            <p>This reed isn't cached locally. We'll load it when you're back online.</p>
          </div>
        {:else if loadingReed}
          <div class="loading">
            <h2>Loading reed...</h2>
            <p>Please wait while we fetch the reed details.</p>
          </div>
        {:else if errorMessage}
          <div class="error-state">
            <div class="error-icon">⚠️</div>
            <h3>Error</h3>
            <p>{errorMessage}</p>
            <button class="btn btn-primary" on:click={() => goto('/reeds')}>Go Back</button>
          </div>
        {:else if reed}
          <div class="reed-detail">
            <div class="reed-meta">
              <div class="reed-author">
                <a href="/profile/{userID}" class="author-avatar">
                  <Avatar userID={userID} username={authorUser?.username ?? userID} size="48px" />
                </a>
                <div class="author-info">
                  <a href="/profile/{userID}" class="author-name">{authorUser?.username ?? userID}</a>
                  <p class="reed-date" class:pending={isPending}>{isPending ? 'Pending…' : formatAbsoluteDateTime(reed.serverSignature?.timestamp)}</p>
                  {#if !isPending}
                    <p class="reed-stats" title="Stats for nerds">
                      <span class="reed-stat-icon echoes" aria-hidden="true"></span>
                      {echoCount}
                    </p>
                  {/if}
                </div>
              </div>
              <div class="reed-actions">
                <button class="reed-menu" on:click={deleteReed}>
                  <span class="menu-dots">🗑️</span>
                </button>
              </div>
            </div>

            <div class="reed-body">
              {#if reed.replying}
                <div class="quote-container">
                  <Quote reed={repliedToReed} missing={repliedToReedMissing} type="reply" linked={true} />
                </div>
              {/if}
              {#if reed.content}
                <MarkdownParser text={reed.content} />
              {/if}
              {#if reed.echoing}
                <div class="quote-container">
                  <Quote reed={echoedReed} missing={echoedReedMissing} type="echo" linked={true} />
                </div>
              {/if}
            </div>

            <div class="reed-actions-bar">
              <button class="action-btn" on:click={handleEcho} aria-label="Echo" disabled={isPending}>
                <span class="action-icon">📢</span>
                <span class="action-label">Echo</span>
              </button>
              <button class="action-btn" on:click={handleReply} aria-label="Reply" disabled={isPending}>
                <span class="action-icon">↩️</span>
                <span class="action-label">Reply</span>
              </button>
              <button class="action-btn" on:click={handleShare} aria-label="Share" disabled={isPending}>
                <span class="action-icon">🔗</span>
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
        {/if}
      </div>

      <!-- Bottom Toolbar -->
      <BottomToolbar currentPage="reeds" />
    </div>

    <NewReedModal open={isReplyModalOpen} replyingTo={reed} on:close={() => { isReplyModalOpen = false; }} />
    <NewReedModal open={isEchoModalOpen} echoOf={reed} on:close={() => { isEchoModalOpen = false; }} />
  </Auth>
{/if}

<style>
  .reed-detail-container {
    min-height: calc(100vh - 4rem - 1px);
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
    padding: 1.5rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-author {
    display: flex;
    align-items: center;
    gap: 1rem;
  }

  .author-avatar {
    width: 48px;
    height: 48px;
    border-radius: 50%;
    overflow: hidden;
    display: flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
    text-decoration: none;
  }

  .author-name {
    display: block;
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
    text-decoration: none;
  }

  .author-name:hover {
    text-decoration: underline;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .reed-date.pending {
    color: var(--primary);
    font-style: italic;
  }

  .reed-stats {
    display: inline-flex;
    align-items: end;
    gap: 0.3rem;
    margin: 0.25rem 0 0;
    color: var(--muted);
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    letter-spacing: 0.02em;
    opacity: 0.8;
  }

  .reed-stat-icon {
    display: inline-block;
    width: 0.85rem;
    height: 0.85rem;
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

  .reed-actions {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-menu {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    border-radius: 4px;
    transition: background-color 0.2s ease;
  }

  .reed-menu:hover {
    background: var(--border);
  }

  .menu-dots {
    font-size: 1.2rem;
    color: var(--muted);
  }

  .reed-body {
    padding: 1.5rem;
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
      padding: 1rem;
    }

    .reed-body {
      padding: 1rem;
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
