<script>
  import { onMount } from 'svelte';
  import { getMentionItems } from '$lib/repositories/mentions';
  import { mentionReedQueue } from '$lib/repositories/reeds';
  import { formatRelativeTime } from '$lib/utils/time';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import { goto } from '$app/navigation';
  import { restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** Already resolved by the route's load() — rendered on first paint, no
   * loading flash (matches the follow/broadcast tabs' pattern). */
  export let items = /** @type {import('$lib/repositories/mentions').ResolvedMentionItem[]} */ ([]);
  /** Window scrollY to restore after the first load (SvelteKit page snapshot). */
  export let scrollRestoreY = /** @type {number | null} */ (null);

  let appliedScrollRestore = false;
  let showNewMentionsBanner = false;
  let lastHandledMentionId = /** @type {string | undefined} */ (undefined);

  $: mentionArrived = $mentionReedQueue?.reed;
  $: if (mentionArrived && mentionArrived.id !== lastHandledMentionId) {
    lastHandledMentionId = mentionArrived.id;
    if (window.scrollY === 0) {
      void reloadMentions();
    } else {
      showNewMentionsBanner = true;
    }
  }

  onMount(async () => {
    if (typeof scrollRestoreY === 'number' && !appliedScrollRestore) {
      appliedScrollRestore = true;
      await restoreWindowScroll(scrollRestoreY);
    }
  });

  async function reloadMentions() {
    try {
      showNewMentionsBanner = false;
      items = await getMentionItems();
    } catch (error) {
      console.error('Error loading mentions:', error);
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.id}`);
  }
</script>

{#if showNewMentionsBanner}
  <div class="new-reed-banner">
    <div class="new-reed-msg">New mentions available</div>
    <button on:click={() => void reloadMentions()}>Show</button>
    <button class="dismiss" on:click={() => (showNewMentionsBanner = false)}>✕</button>
  </div>
{/if}

<div class="reeds-list">
  {#if items.length === 0}
    <div class="empty-state">
      <div class="empty-icon">📣</div>
      <h3>No mentions yet</h3>
      <p>Reeds that mention you will appear here.</p>
    </div>
  {:else}
    {#each items as item (item.record.reedID)}
      <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(item.reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(item.reed)}>
        <div class="reed-header">
          <ReedAuthorHeader
            userID={item.reed.userID}
            username={item.author.username}
            nameTag="h3"
            subtext={`Mentioned you ${formatRelativeTime(item.record.createdAt)}`}
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
  .new-reed-banner {
    position: fixed;
    top: 1rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    background: var(--surface);
    border: 1px solid var(--primary);
    border-radius: 8px;
    font-size: 0.9rem;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    width: calc(100vw - 2.5rem);
  }

  .new-reed-banner .new-reed-msg {
    flex-grow: 1;
    color: var(--fg);
  }

  .new-reed-banner button {
    flex-shrink: 0;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    border-radius: 4px;
    padding: 0.25rem 0.75rem;
    cursor: pointer;
    font-size: 0.85rem;
    white-space: nowrap;
    width: 5rem;
  }

  .new-reed-banner button.dismiss {
    flex-shrink: 0;
    background: none;
    color: var(--muted);
    padding: 0.25rem;
    cursor: pointer;
    width: 2rem;
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
