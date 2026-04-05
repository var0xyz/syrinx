<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { reedsService, stripMarkdown, formatRelativeTime } from '$lib/repositories/reeds';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import { userRepository } from '$lib/repositories/user';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import { goto } from '$app/navigation';

  let user = null;
  let loading = true;
  let isWriteSectionOpen = false;

  // Reed list state
  let reeds = [];
  let loadingReeds = true;
  let errorLoadingReeds = '';
  let echoedReeds = new Map();
  let repliedToReeds = new Map();

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();

      if (!user) {
        // Redirect to home page if no user
        window.location.href = '/';
        return;
      }

      // Load user's reeds
      await loadReeds();

    } catch (error) {
      console.error('Error getting user:', error);
      // Redirect to home page on error
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });

  async function loadReeds() {
    try {
      loadingReeds = true;
      errorLoadingReeds = '';
      reeds = await reedsService.getReedsByAuthor(user.id);

      // Fetch echoed reeds in parallel
      const echoEntries = reeds
        .filter(r => r.headers.echoing)
        .map(r => {
          const sep = r.headers.echoing.lastIndexOf('!');
          const author = r.headers.echoing.substring(0, sep);
          const reedId = r.headers.echoing.substring(sep + 1);
          return { key: r.headers.echoing, author, reedId };
        });

      const echoResults = await Promise.allSettled(
        echoEntries.map(({ author, reedId }) => reedsService.getReed(author, reedId))
      );

      const echoMap = new Map();
      echoEntries.forEach(({ key }, i) => {
        if (echoResults[i].status === 'fulfilled' && echoResults[i].value) {
          echoMap.set(key, echoResults[i].value);
        }
      });
      echoedReeds = echoMap;

      // Fetch replied-to reeds in parallel
      const replyEntries = reeds
        .filter(r => r.headers.replying)
        .map(r => {
          const sep = r.headers.replying.lastIndexOf('!');
          const author = r.headers.replying.substring(0, sep);
          const reedId = r.headers.replying.substring(sep + 1);
          return { key: r.headers.replying, author, reedId };
        });

      const replyResults = await Promise.allSettled(
        replyEntries.map(({ author, reedId }) => reedsService.getReed(author, reedId))
      );

      const replyMap = new Map();
      replyEntries.forEach(({ key }, i) => {
        if (replyResults[i].status === 'fulfilled' && replyResults[i].value) {
          replyMap.set(key, replyResults[i].value);
        }
      });
      repliedToReeds = replyMap;
    } catch (error) {
      console.error('Error loading reeds:', error);
      errorLoadingReeds = 'Failed to load reeds';
    } finally {
      loadingReeds = false;
    }
  }

  function toggleWriteSection() {
    isWriteSectionOpen = !isWriteSectionOpen;
  }

  function closeModal() {
    isWriteSectionOpen = false;
  }

  async function deleteReed(reedId) {
    if (!confirm('Are you sure you want to delete this reed?')) {
      return;
    }

    await performDelete(reedId);
  }

  async function performDelete(reedId) {
    try {
      // Delete from server
      await apiService.deleteReed(user.id, reedId);

      // Delete from IndexedDB
      await dbService.delete('reeds', reedId);

      // Remove from local state
      reeds = reeds.filter(reed => reed.headers.id !== reedId);
    } catch (error) {
      console.error('Error deleting reed:', error);
      errorMessage = 'Failed to delete reed';
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.headers.author}/${reed.headers.id}`);
  }

</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading reeds...</h2>
        <p>Please wait while we fetch your message threads.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="reeds-container">
    <!-- Floating Write Button -->
    <button class="floating-write-btn" on:click={toggleWriteSection}>
      <span class="icon">✍️</span>
    </button>

    <!-- Write Modal -->
    <NewReedModal open={isWriteSectionOpen} on:close={closeModal} />

    <!-- Main Content -->
    <div class="reeds-content">
      <div class="reeds-list">
        {#if loadingReeds}
          <div class="loading">
            <h2>Loading reeds...</h2>
            <p>Please wait while we fetch your reeds.</p>
          </div>
        {:else if errorLoadingReeds}
          <div class="error-state">
            <div class="error-icon">⚠️</div>
            <h3>Error loading reeds</h3>
            <p>{errorLoadingReeds}</p>
            <button class="btn btn-primary" on:click={loadReeds}>Try Again</button>
          </div>
        {:else if reeds.length === 0}
          <div class="empty-state">
            <div class="empty-icon">🌾</div>
            <h3>No reeds yet</h3>
            <p>Your reeds will appear here when you publish them.</p>
          </div>
        {:else}
          {#each reeds as reed (reed.headers.id)}
            <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(reed)}>
              <div class="reed-header">
                <div class="reed-info">
                  <div class="reed-avatar">
                    {#if user.avatarURL}
                      <img src={user.avatarURL} alt={user.username} />
                    {:else}
                      <div class="reed-icon">👤</div>
                    {/if}
                  </div>
                  <div class="reed-details">
                    <h3>{user.username}</h3>
                    <p>{formatRelativeTime(reed.headers.timestamp)}</p>
                  </div>
                </div>
                <div class="reed-meta">
                  <button class="reed-menu" on:click|stopPropagation={() => deleteReed(reed.headers.id)} aria-label="Delete">🗑️</button>
                </div>
              </div>
              {#if reed.headers.replying}
                <div class="quote-container">
                  <Quote
                    reed={repliedToReeds.get(reed.headers.replying)}
                    type="reply"
                    missing={!repliedToReeds.has(reed.headers.replying)}
                  />
                </div>
              {/if}
              {#if (reed.content || "").trim()}
                <div class={["reed-preview", reed.headers.echoing && "echo", reed.headers.replying && "reply"]}>
                  <p>{stripMarkdown(reed.content)}</p>
                </div>
              {/if}
              {#if reed.headers.echoing}
                <div class="quote-container">
                  <Quote
                    reed={echoedReeds.get(reed.headers.echoing)}
                    type="echo"
                    missing={!echoedReeds.has(reed.headers.echoing)}
                  />
                </div>
              {/if}
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="reeds" />
    </div>
  </Auth>
{/if}

<style>
  .reeds-container {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }


  .floating-write-btn {
    position: fixed;
    bottom: 80px;
    right: 20px;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
  }

  .floating-write-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-write-btn .icon {
    font-size: 1.5rem;
  }

  .reeds-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .reeds-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .reed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
    cursor: pointer;
  }

  .reed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .reed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-info {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .reed-icon {
    width: 40px;
    height: 40px;
    border-radius: 8px;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.2rem;
  }

  .reed-avatar {
    width: 40px;
    height: 40px;
    border-radius: 8px;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--input-bg);
  }

  .reed-avatar img {
    width: 100%;
    height: 100%;
    object-fit: cover;
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

  .reed-details h3 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1rem;
    font-weight: 600;
  }

  .reed-details p {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .reed-meta {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
  }

  .reed-preview.reply {
    padding-top: 0;
    padding-left: 2rem;
  }

  .reed-preview.echo {
    padding-bottom: 0;
  }

  .reed-preview p {
    margin: 0;
    line-height: 1.4;
    font-size: 0.9rem;
  }

  .quote-container {
    margin: 1rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
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

  /* Responsive Design */
  @media (max-width: 768px) {
    .reeds-list {
      gap: 0.5rem;
    }

    .reeds-content {
      padding: 0.5rem;
    }

    .reed-header {
      padding: 0.75rem;
    }

    .reed-preview {
      padding: 0.75rem;
    }

    .reed-preview.reply {
      padding-top: 0;
      padding-left: 1.5rem;
    }

    .reed-preview.echo {
    }

    .quote-container {
      margin: 0.75rem;
    }
  }
</style>
