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
  /** Pages to open with, for restoring a list the user had paged into.
   * Bindable, so a caller can snapshot the current depth on navigate-away. */
  export let depth = 1;

  const dispatch = createEventDispatcher<{ ready: void }>();

  /** Bindable: lets a caller read the accumulated items (e.g. for a
   * bound item-count display) without duplicating pagination state. */
  export let items: T[] = [];
  let loading = true;
  let loadingMore = false;
  let hasMore = false;
  let cursor: string | undefined;
  let error = '';
  /** Pages actually walked, so a restored depth can be told apart from the
   * depth this component itself just reported. */
  let walkedDepth = 0;

  let mounted = false;
  onMount(async () => {
    mounted = true;
    await loadFirstPage();
  });

  // A snapshot restore sets depth after this component has already mounted
  // and loaded one page, so deepen to match instead of missing it.
  $: if (mounted && depth > walkedDepth) void deepenTo(depth);

  async function deepenTo(target: number) {
    await walkPages(target);
    // Signals the caller to re-apply anything that depends on full height,
    // such as a restored scroll position.
    dispatch('ready');
  }

  export async function loadFirstPage() {
    loading = true;
    error = '';
    try {
      await walkPages(depth);
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

  /** Read `count` pages from the start, replacing what's loaded. depth is
   * how many pages the user asked for, never how many items came back —
   * a sparsely-filled page still counts as a page. */
  async function walkPages(count: number) {
    const want = Math.max(1, count);
    let acc: T[] = [];
    let next: string | undefined;
    let more = false;
    let walked = 0;
    for (let i = 0; i < want; i++) {
      const page = await fetchPage(next);
      acc = [...acc, ...page.items];
      more = page.hasMore;
      next = page.nextCursor;
      walked = i + 1;
      if (!more) break;
    }
    items = acc;
    hasMore = more;
    cursor = next;
    walkedDepth = walked;
    depth = walked;
  }

  /** Re-walk the pages already shown, keeping the same depth, so newly
   * stored items appear without collapsing back to a single page. */
  export async function reload() {
    try {
      await walkPages(depth);
    } catch (err) {
      console.error('LocalPagination: failed to reload:', err);
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
      walkedDepth += 1;
      depth = walkedDepth;
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
