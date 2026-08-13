<script>
  import { onMount } from 'svelte';
  import { reedsService } from '$lib/repositories/reeds';
  import { likedReedsRepository } from '$lib/repositories/likedReeds';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import { goto } from '$app/navigation';
  import { restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** Window scrollY to restore after the first load (SvelteKit page snapshot). */
  export let scrollRestoreY = /** @type {number | null} */ (null);

  let loading = true;
  /** @type {{ record: import('$lib/repositories/likedReeds').LikedReedRecord, reed: import('$lib/types/reed').ReedType, author: any }[]} */
  let items = [];
  let appliedScrollRestore = false;

  onMount(async () => {
    await loadLiked();
  });

  async function loadLiked() {
    try {
      loading = true;
      const records = await likedReedsRepository.getAll();

      const resolved = await Promise.allSettled(
        records.map((record) => reedsService.getReed(record.authorID, record.reedID))
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

      items = withReeds.map(({ record, reed }) => ({
        record,
        reed,
        author: authorMap.get(reed.userID) || { username: reed.userID },
      }));
    } catch (error) {
      console.error('Error loading liked reeds:', error);
      items = [];
    } finally {
      loading = false;
      if (!appliedScrollRestore && typeof scrollRestoreY === 'number') {
        appliedScrollRestore = true;
        await restoreWindowScroll(scrollRestoreY);
      }
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.userID}/${reed.id}`);
  }
</script>

<div class="reeds-list">
  {#if loading}
    <div class="loading">
      <h2>Loading liked reeds...</h2>
    </div>
  {:else if items.length === 0}
    <div class="empty-state">
      <div class="empty-icon">💖</div>
      <h3>No liked reeds yet</h3>
      <p>Reeds you like will appear here.</p>
    </div>
  {:else}
    {#each items as item (item.record.compositeKey)}
      <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(item.reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(item.reed)}>
        <div class="reed-header">
          <ReedAuthorHeader
            userID={item.reed.userID}
            serverID={item.reed.serverSignature?.serverID ?? ''}
            username={item.author.username}
            nameTag="h3"
            subtext={formatRelativeTime(item.record.likedAt)}
            stopPropagation
            linked={false}
          />
        </div>
        {#if item.reed.replying}
          <div class="quote-container">
            <Quote reedRef={item.reed.replying} type="reply" missing={false} linked={false} />
          </div>
        {/if}
        {#if (item.reed.content || '').trim()}
          <div class="reed-preview">
            <MarkdownParser text={item.reed.content} preview={true} />
          </div>
        {/if}
        {#if item.reed.echoing}
          <div class="quote-container">
            <Quote reedRef={item.reed.echoing} type="echo" missing={false} linked={false} />
          </div>
        {/if}
      </div>
    {/each}
  {/if}
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

  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
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
