<script>
  import { onDestroy, onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';

  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let reedID;

  /** Bound out to the parent for tab-count display. */
  export let count = 0;

  /** @type {{ userID: string; username: string; serverID: string; echoedAt: string }[]} */
  let rows = [];
  let loading = true;
  let loadingMore = false;
  let error = '';
  let hasMore = false;
  /** @type {string | undefined} */
  let cursor = undefined;

  $: count = rows.length;

  async function resolveRow(u) {
    const profile = await userRepository.getByUserId(u.userID).catch(() => null);
    return {
      userID: u.userID,
      username: profile?.username ?? u.userID,
      serverID: profile?.serverSignature?.serverID ?? '',
      echoedAt: u.echoedAt,
    };
  }

  async function loadPage() {
    const list = await apiService.listEchoers(userID, reedID, { before: cursor });
    const resolved = await Promise.all(list.users.map(resolveRow));
    rows = [...rows, ...resolved];
    hasMore = list.hasMore;
    cursor = list.users.length > 0 ? list.users[list.users.length - 1].echoedAt : cursor;
  }

  async function loadMore() {
    if (!hasMore || loadingMore) return;
    loadingMore = true;
    try {
      await loadPage();
    } catch (err) {
      console.error('Failed to load more of the chorus:', err);
    } finally {
      loadingMore = false;
    }
  }

  /** REED_ECHOES only carries the new total, not who echoed — refetch from
   * the top rather than trying to patch `rows` in place. Collapses any
   * pagination the viewer had done back to the first page, same tradeoff
   * ConversationSection makes on a live reply. */
  async function reload() {
    try {
      rows = [];
      hasMore = false;
      cursor = undefined;
      await loadPage();
    } catch (err) {
      console.error('Failed to refresh chorus:', err);
    }
  }

  function handleReedEchoes(msg) {
    if (msg?.userID !== userID || msg?.reedID !== reedID) return;
    void reload();
  }

  onMount(async () => {
    try {
      await loadPage();
    } catch (err) {
      console.error('Failed to load chorus:', err);
      error = 'Unable to load this list right now.';
    } finally {
      loading = false;
    }
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
            serverID={row.serverID}
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
      <button type="button" class="load-more-btn" on:click={loadMore} disabled={loadingMore}>
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
