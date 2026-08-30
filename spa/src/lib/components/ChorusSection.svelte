<script>
  import { onDestroy, onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import { createPaginationStore } from '$lib/stores/pagination';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';

  /** The reed's canonical id (authorID@serverID/uuid). */
  /** @type {string} */
  export let reedID;

  /** Bound out to the parent for tab-count display. */
  export let count = 0;

  async function resolveRow(u) {
    const profile = await userRepository.getByUserId(u.userID).catch(() => null);
    return {
      userID: u.userID,
      username: profile?.username ?? u.userID,
      echoedAt: u.echoedAt,
    };
  }

  const store = createPaginationStore({
    fetchPage: (cursor) => apiService.listEchoers(reedID, { before: cursor }),
    getItems: (page) => Promise.all(page.users.map(resolveRow)),
    getHasMore: (page) => page.hasMore,
    getNextCursor: (page) =>
      page.users.length > 0 ? page.users[page.users.length - 1].echoedAt : undefined,
  });

  $: ({ rows, loading, loadingMore, hasMore, error } = $store);
  $: count = rows.length;

  function handleReedEchoes(msg) {
    if (msg?.reedID !== reedID) return;
    void store.reload();
  }

  onMount(async () => {
    await store.load();
  });

  onMount(() => {
    serverConnection.on(ServerEvent.ReedEchoes, handleReedEchoes);
  });

  onDestroy(() => {
    serverConnection.off(ServerEvent.ReedEchoes, handleReedEchoes);
  });
</script>

<section class="chorus-section" aria-label="Chorus">
  {#if loading}
    <p class="chorus-empty">Loading…</p>
  {:else if error && rows.length === 0}
    <p class="chorus-empty error">{error}</p>
  {:else if rows.length === 0}
    <p class="chorus-empty">No one has echoed this yet.</p>
  {:else}
    <div class="chorus-list">
      {#each rows as row (row.userID)}
        <div
          class="chorus-row"
          role="button"
          tabindex="0"
          on:click={() => goto(`/profile/${row.userID}`)}
          on:keydown={(e) => e.key === 'Enter' && goto(`/profile/${row.userID}`)}
        >
          <ReedAuthorHeader
            userID={row.userID}
            username={row.username}
            subtext={`Echoed ${formatRelativeTime(row.echoedAt)}`}
            stopPropagation
          />
        </div>
      {/each}
    </div>
    {#if error}
      <p class="chorus-empty error">{error}</p>
    {/if}
    {#if hasMore}
      <button type="button" class="load-more-btn" on:click={store.loadMore} disabled={loadingMore}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </button>
    {/if}
  {/if}
</section>

<style>
  .chorus-section {
    padding-top: 1rem;
  }

  .chorus-empty {
    margin: 0 0.75rem 1rem;
    color: var(--muted);
    font-size: 0.9rem;
    font-style: italic;
  }

  .chorus-empty.error {
    color: var(--error);
  }

  .chorus-list {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0 0.75rem;
    display: flex;
    flex-direction: column;
  }

  .chorus-row {
    padding: 0.4rem;
    border-radius: 8px;
    cursor: pointer;
  }

  .chorus-row:hover {
    background: var(--input-bg);
  }

  .load-more-btn {
    margin: 0 0.75rem 1rem;
    font-size: 0.8rem;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    border-radius: 8px;
    padding: 0.5rem;
    cursor: pointer;
    font-weight: 600;
  }

  .load-more-btn:hover:not(:disabled) {
    background: var(--input-bg);
  }

  .load-more-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
