<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { reedsService, stripMarkdown, formatAbsoluteDateTime, countMarkdownCharacters } from '$lib/repositories/reeds';
  import { Reed } from '$lib/types/reed';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { goto } from '$app/navigation';
  import { notificationStore } from '$lib/stores/notifications';

  let user = null;
  let loading = true;
  let reed = null;
  let loadingReed = true;
  let errorMessage = '';
  let echoedReed = null;
  let echoedReedMissing = false;
  let repliedToReed = null;
  let repliedToReedMissing = false;

  // Action buttons state
  let likesCount = 0;
  let isLiked = false;
  let isReplyModalOpen = false;
  let isEchoModalOpen = false;

  $: userID = $page.params.userID;
  $: reedID = $page.params.reedID;

  $: if (reedID && user) loadReed();

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

  async function loadReed() {
    try {
      loadingReed = true;
      errorMessage = '';
      reed = await reedsService.getReed(userID, reedID);
      console.log('Reed:', reed);

      if (!reed) {
        errorMessage = 'Reed not found';
        return;
      }

      // Verify that the user matches
      if (reed.headers.author !== userID) {
        errorMessage = 'This reed does not belong to the specified user';
        return;
      }

      // Load echoed reed if this is an echo
      echoedReed = null;
      echoedReedMissing = false;
      if (reed.headers.echoing) {
        const [echoAuthor, echoId] = reed.headers.echoing.split('!');
        try {
          echoedReed = await reedsService.getReed(echoAuthor, echoId);
          if (!echoedReed) {
            const data = await apiService.getReed(echoAuthor, echoId);
            echoedReed = data;
          }
          if (!echoedReed) echoedReedMissing = true;
        } catch {
          echoedReedMissing = true;
        }
      }

      // Load replied-to reed if this is a reply
      repliedToReed = null;
      repliedToReedMissing = false;
      if (reed.headers.replying) {
        try {
          repliedToReed = await reedsService.getReed('', reed.headers.replying);
          if (!repliedToReed) repliedToReedMissing = true;
        } catch {
          repliedToReedMissing = true;
        }
      }
    } catch (error) {
      console.error('Error loading reed:', error);
      errorMessage = 'Failed to load reed';
    } finally {
      loadingReed = false;
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
      // Delete from server
      await apiService.deleteReed(userID, reedID);

      // Delete from IndexedDB
      await dbService.delete('reeds', reedID);

      // Navigate back to reeds list
      goto('/reeds');
    } catch (error) {
      console.error('Error deleting reed:', error);
      errorMessage = 'Failed to delete reed';
    }
  }

  // Action button handlers
  function handleEcho() {
    isEchoModalOpen = true;
  }

  function handleReply() {
    isReplyModalOpen = true;
  }

  async function handleShare() {
    if (!reed) return;

    const reedUrl = `${window.location.origin}/reed/${userID}/${reedID}`;
    const reedText = stripMarkdown(reed.content);
    const shareData = {
      title: `${user?.username || 'User'}'s Reed`,
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
        {#if loadingReed}
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
                <div class="author-avatar">
                  {#if user.avatarURL}
                    <img src={user.avatarURL} alt={user.username} />
                  {:else}
                    <div class="avatar-icon">👤</div>
                  {/if}
                </div>
                <div class="author-info">
                  <h2>{user.username}</h2>
                  <p class="reed-date">{formatAbsoluteDateTime(reed.headers.timestamp)}</p>
                </div>
              </div>
              <div class="reed-actions">
                <button class="reed-menu" on:click={deleteReed}>
                  <span class="menu-dots">🗑️</span>
                </button>
              </div>
            </div>

            <div class="reed-body">
              {#if reed.headers.replying}
                <div class="quote-container">
                  <Quote reed={repliedToReed} missing={repliedToReedMissing} type="reply" linked={true} />
                </div>
              {/if}
              {#if reed.content}
                <MarkdownParser text={reed.content} />
              {/if}
              {#if reed.headers.echoing}
                <div class="quote-container">
                  <Quote reed={echoedReed} missing={echoedReedMissing} type="echo" linked={true} />
                </div>
              {/if}
            </div>

            <div class="reed-actions-bar">
              <button class="action-btn" on:click={handleEcho} aria-label="Echo">
                <span class="action-icon">📢</span>
                <span class="action-label">Echo</span>
              </button>
              <button class="action-btn" on:click={handleReply} aria-label="Reply">
                <span class="action-icon">↩️</span>
                <span class="action-label">Reply</span>
              </button>
              <button class="action-btn" on:click={handleShare} aria-label="Share">
                <span class="action-icon">🔗</span>
                <span class="action-label">Share</span>
              </button>
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
    min-height: 100vh;
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
    align-items: center;
    justify-content: center;
    background: var(--input-bg);
  }

  .author-avatar img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .avatar-icon {
    font-size: 1.5rem;
  }

  .author-info h2 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
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

  .reed-body p {
    margin: 0;
    color: var(--fg);
    line-height: 1.6;
    font-size: 1rem;
    white-space: pre-wrap;
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
