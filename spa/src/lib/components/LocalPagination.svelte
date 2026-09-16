<script lang="ts" generics="T">
  import { createEventDispatcher, onMount } from 'svelte';
  import type { Snippet } from 'svelte';

  /** Reads one page from a local IndexedDB-backed source. after is
   * undefined for the first page; pass the previous page's nextCursor to
   * resume. hasMore has no native signal from IndexedDB — the caller's
   * function is expected to do its own limit+1-fetch-and-slice trick
   * (see likedReedsRepository.getPage) and report the result here. */
  export let fetchPage: (after?: string) => Promise<{ items: T[]; hasMore: boolean; nextCursor?: string }>;
  export let item: Snippet<[T]>;
  export let empty: Snippet | undefined = undefined;
  export let errorMessage = 'Unable to load this list right now.';
  export let buttonClass = 'load-more-btn';

  const dispatch = createEventDispatcher<{ ready: void }>();

  /** Bindable: lets a caller read the accumulated items (e.g. for a
   * bound item-count display) without duplicating pagination state. */
  export let items: T[] = [];
  let loading = true;
  let loadingMore = false;
  let hasMore = false;
  let cursor: string | undefined;
  let error = '';

  onMount(loadFirstPage);

  export async function loadFirstPage() {
    loading = true;
    error = '';
    try {
      const page = await fetchPage(undefined);
      items = page.items;
      hasMore = page.hasMore;
      cursor = page.nextCursor;
    } catch (err) {
      console.error('LocalPagination: failed to load first page:', err);
      items = [];
      hasMore = false;
      error = errorMessage;
    } finally {
      loading = false;
      dispatch('ready');
    }
  }

  async function loadMore() {
    if (loadingMore || !hasMore) return;
    loadingMore = true;
    try {
      const page = await fetchPage(cursor);
      items = [...items, ...page.items];
      hasMore = page.hasMore;
      cursor = page.nextCursor;
    } catch (err) {
      console.error('LocalPagination: failed to load more:', err);
    } finally {
      loadingMore = false;
    }
  }
</script>

{#if loading}
  <div class="pagination-loading">
    <p>Loading…</p>
  </div>
{:else if error && items.length === 0}
  <p class="pagination-error">{error}</p>
{:else if items.length === 0}
  {#if empty}
    {@render empty()}
  {/if}
{:else}
  {#each items as row}
    {@render item(row)}
  {/each}
  {#if error}
    <p class="pagination-error">{error}</p>
  {/if}
  {#if hasMore}
    <button type="button" class={buttonClass} on:click={loadMore} disabled={loadingMore}>
      {loadingMore ? 'Loading…' : 'Load more'}
    </button>
  {/if}
{/if}

<style>
  .pagination-loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .pagination-error {
    text-align: center;
    padding: 1rem;
    color: var(--danger, #e5484d);
  }

  .load-more-btn {
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    border-radius: 8px;
    padding: 0.5rem;
    cursor: pointer;
    font-weight: 600;
    width: 100%;
  }

  .load-more-btn:hover:not(:disabled) {
    background: var(--input-bg);
  }

  .load-more-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
