<script>
  import { reedsService } from '$lib/repositories/reeds';
  import { likedReedsRepository } from '$lib/repositories/likedReeds';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import LocalPagination from '$lib/components/LocalPagination.svelte';
  import { goto } from '$app/navigation';
  import { restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** Window scrollY to restore after the first load (SvelteKit page snapshot). */
  export let scrollRestoreY = /** @type {number | null} */ (null);

  const PAGE_SIZE = 50;

  let appliedScrollRestore = false;

  /** Resolves reed+author for a page of liked-reed records, dropping any
   * whose reed isn't locally held. */
  async function resolveItems(records) {
    const resolved = await Promise.allSettled(
      records.map((record) => reedsService.getReed(record.reedID))
    );

    const withReeds = [];
    records.forEach((record, i) => {
      const result = resolved[i];
      if (result.status === 'fulfilled' && result.value) {
        withReeds.push({ record, reed: result.value });
      }
    });

    const authorIds = [...new Set(withReeds.map((item) => item.reed.userID))];
    const authorResults = await Promise.allSettled(
      authorIds.map((id) => userRepository.getByUserId(id))
    );
    const authorMap = new Map();
    authorIds.forEach((id, i) => {
      const result = authorResults[i];
      if (result.status === 'fulfilled' && result.value) {
        authorMap.set(id, result.value);
      }
    });

    return withReeds.map(({ record, reed }) => ({
      record,
      reed,
      author: authorMap.get(reed.userID) || { username: reed.userID },
    }));
  }

  async function fetchLikedPage(after) {
    // Fetch one extra record to detect whether another page exists,
    // without it every page boundary would masquerade as the last.
    const records = await likedReedsRepository.getPage(PAGE_SIZE + 1, after);
    const hasMore = records.length > PAGE_SIZE;
    const pageRecords = records.slice(0, PAGE_SIZE);
    const items = await resolveItems(pageRecords);
    const nextCursor = pageRecords.length > 0 ? pageRecords[pageRecords.length - 1].likedAt : after;
    return { items, hasMore, nextCursor };
  }

  function onFirstPageSettled() {
    if (!appliedScrollRestore && typeof scrollRestoreY === 'number') {
      appliedScrollRestore = true;
      void restoreWindowScroll(scrollRestoreY);
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.id}`);
  }
</script>

<div class="reeds-list">
  <LocalPagination fetchPage={fetchLikedPage} on:ready={onFirstPageSettled}>
    {#snippet item(likedItem)}
      <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(likedItem.reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(likedItem.reed)}>
        <div class="reed-header">
          <ReedAuthorHeader
            userID={likedItem.reed.userID}
            username={likedItem.author.username}
            nameTag="h3"
            subtext={`Liked ${formatRelativeTime(likedItem.record.likedAt)}`}
            stopPropagation
            linked={false}
          />
        </div>
        {#if likedItem.reed.replying}
          <div class="quote-container">
            <Quote reedRef={likedItem.reed.replying} type="reply" missing={false} linked={false} />
          </div>
        {/if}
        {#if (likedItem.reed.content || '').trim()}
          <div class="reed-preview">
            <MarkdownParser text={likedItem.reed.content} preview={true} />
          </div>
        {/if}
        {#if likedItem.reed.echoing}
          <div class="quote-container">
            <Quote reedRef={likedItem.reed.echoing} type="echo" missing={false} linked={false} />
          </div>
        {/if}
      </div>
    {/snippet}
    {#snippet empty()}
      <div class="empty-state">
        <div class="empty-icon">💖</div>
        <h3>No liked reeds yet</h3>
        <p>Reeds you like will appear here.</p>
      </div>
    {/snippet}
  </LocalPagination>
</div>

<style>
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
    min-width: 0;
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
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

  @media (max-width: 768px) {
    .reeds-list {
      gap: 0.5rem;
    }

    .reed-header {
      padding: 0.75rem;
    }

    .reed-preview {
      padding: 0.5rem 0.75rem;
    }

    .quote-container {
      margin: 0.75rem;
    }
  }
</style>
