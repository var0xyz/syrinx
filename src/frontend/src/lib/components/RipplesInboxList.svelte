<script>
  // Flat, unthreaded, read-only list of received ripples (GET /ripples)
  // across many reeds — unlike RipplesSection, scoped to one reed's thread.
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import { ripplesRepository } from '$lib/repositories/ripples';
  import { formatRelativeTime, formatAbsoluteDateTime } from '$lib/utils/time';

  /** @type {import('$lib/types/api').ReceivedRipple[]} */
  let ripples = [];
  /** username resolved per userID (commenter or reed author), null if
   * unresolvable (removed account). */
  let usernames = {};

  let loading = true;
  let error = false;
  let hasMore = false;
  let loadingMore = false;
  let nextCursor = /** @type {string | undefined} */ (undefined);

  let ownUserID = '';
  onMount(() => {
    ownUserID = localStorage.getItem('userId') ?? '';
  });

  async function resolveUsername(uid) {
    if (!uid || uid in usernames) return;
    const user = await userRepository.getByUserId(uid).catch(() => null);
    usernames = { ...usernames, [uid]: user?.username ?? null };
  }

  async function loadPage(before) {
    const res = await apiService.getReceivedRipples({ limit: 50, before });

    const kept = [];
    for (const ripple of res.ripples) {
      const ok = await ripplesRepository.storeRipple(ripple, ripple.reedID);
      if (ok) kept.push(ripple);
    }
    for (const ripple of kept) {
      await resolveUsername(ripple.userID);
      await resolveUsername(ripple.reedAuthorID);
    }
    ripples = before ? [...ripples, ...kept] : kept;
    hasMore = res.hasMore;
    nextCursor = res.nextCursor;
  }

  async function loadMore() {
    if (!hasMore || loadingMore || !nextCursor) return;
    loadingMore = true;
    try {
      await loadPage(nextCursor);
    } catch (err) {
      console.error('Failed to load more ripples:', err);
    } finally {
      loadingMore = false;
    }
  }

  onMount(async () => {
    try {
      await loadPage(undefined);
    } catch (err) {
      console.error('Failed to load ripples inbox:', err);
      error = true;
    } finally {
      loading = false;
    }
  });

  function openReed(reedID) {
    goto(`/reed/${reedID}`);
  }
</script>

<div class="ripples-inbox">
  {#if loading}
    <p class="inbox-empty">Loading…</p>
  {:else if error}
    <p class="inbox-empty">Couldn't load your ripples. Try again later.</p>
  {:else if ripples.length === 0}
    <div class="empty-state">
      <div class="empty-icon">🌊</div>
      <h3>No ripples yet</h3>
      <p>Comments on your reeds, and replies to your own comments, will appear here.</p>
    </div>
  {:else}
    <ul class="inbox-list">
      {#each ripples as ripple (ripple.hash)}
        <li
          class="inbox-row"
          role="button"
          tabindex="0"
          on:click={() => openReed(ripple.reedID)}
          on:keydown={(e) => e.key === 'Enter' && openReed(ripple.reedID)}
        >
          <div class="inbox-avatar">
            <Avatar userID={ripple.userID} username={usernames[ripple.userID] ?? ''} size="32px" />
          </div>
          <div class="inbox-body">
            <p class="inbox-meta">
              {#if usernames[ripple.userID]}
                <Username userID={ripple.userID} username={usernames[ripple.userID]} stopPropagation />
              {:else}
                <span class="inbox-username-removed">[removed account]</span>
              {/if}
              <span class="inbox-meta-sep">&middot;</span>
              <span class="inbox-meta-text">{formatRelativeTime(ripple.postedAt)}</span>
            </p>
            <p class="inbox-context">
              {#if ripple.reedAuthorID === ownUserID}
                on your reed
              {:else if usernames[ripple.reedAuthorID]}
                on @{usernames[ripple.reedAuthorID]}'s reed
              {:else}
                on a reed
              {/if}
            </p>
            {#if ripple.deleted}
              <p class="inbox-content inbox-content-deleted">[DELETED]</p>
            {:else}
              <p class="inbox-content">{ripple.content}</p>
            {/if}
            <p class="inbox-expiry">Expires {formatAbsoluteDateTime(ripple.expiresAt)}</p>
          </div>
        </li>
      {/each}
    </ul>
    {#if hasMore}
      <button type="button" class="load-more-btn" on:click={loadMore} disabled={loadingMore}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </button>
    {/if}
  {/if}
</div>

<style>
  .ripples-inbox {
    display: flex;
    flex-direction: column;
  }

  .inbox-empty {
    color: var(--muted);
    font-size: 0.9rem;
    font-style: italic;
    padding: 1rem 0.75rem;
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

  .inbox-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .inbox-row {
    display: flex;
    gap: 0.6rem;
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    cursor: pointer;
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
  }

  .inbox-row:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .inbox-avatar {
    flex: 0 0 auto;
    padding-top: 0.1rem;
  }

  .inbox-body {
    min-width: 0;
    flex: 1 1 auto;
  }

  .inbox-meta {
    display: flex;
    align-items: baseline;
    gap: 0.3rem;
    margin: 0;
    font-size: 0.82rem;
  }

  .inbox-meta-sep {
    color: var(--muted);
  }

  .inbox-meta-text {
    color: var(--muted);
  }

  .inbox-username-removed {
    font-style: italic;
    color: var(--muted);
  }

  .inbox-context {
    margin: 0.1rem 0 0.3rem;
    font-size: 0.78rem;
    color: var(--muted);
    font-style: italic;
  }

  .inbox-content {
    margin: 0;
    font-size: 0.88rem;
    line-height: 1.45;
    color: var(--fg);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .inbox-content-deleted {
    color: var(--muted);
    font-style: italic;
  }

  .inbox-expiry {
    margin: 0.4rem 0 0;
    font-size: 0.75rem;
    color: var(--muted);
  }

  .load-more-btn {
    align-self: center;
    margin: 1rem 0;
    background: none;
    border: none;
    font-size: 0.85rem;
    color: var(--muted);
    cursor: pointer;
  }

  .load-more-btn:hover {
    color: var(--fg);
    text-decoration: underline;
  }

  @media (max-width: 768px) {
    .inbox-row {
      padding: 0.6rem;
    }
  }
</style>
