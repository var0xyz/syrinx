<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { followReedQueue, getFollowReeds } from '$lib/repositories/reeds';
  import { formatRelativeTime } from '$lib/utils/time';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import FeedTabs from '$lib/components/FeedTabs.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import { captureWindowScroll, restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** @type {import('./$types').PageData} */
  export let data;

  let followReeds = data.followReeds;
  $: followReeds = data.followReeds;

  let lastHandledFollowReedId = '';

  $: followArrived = $followReedQueue?.reed;
  $: if (followArrived && followArrived.id !== lastHandledFollowReedId) {
    lastHandledFollowReedId = followArrived.id;
    void loadFollowReeds();
  }

  async function loadFollowReeds() {
    followReeds = await getFollowReeds();
  }

  /** @type {number | null} */
  let pendingScrollY = null;

  /** @type {import('./$types').Snapshot<number>} */
  export const snapshot = {
    capture: () => captureWindowScroll(),
    restore: (y) => {
      pendingScrollY = y;
    },
  };

  onMount(async () => {
    if (pendingScrollY != null) {
      const y = pendingScrollY;
      pendingScrollY = null;
      await restoreWindowScroll(y);
    }
  });
</script>

<Auth>
  <div class="feed-container">
    <FeedTabs active="follow" />

    <div class="feed-content-wrap">
      <div class="reeds-list">
        {#if followReeds.reeds.length === 0}
          <div class="empty-state">
            <div class="empty-icon">👥</div>
            <h3>No reeds from people you follow yet.</h3>
            <p>Follow people and their reeds will appear here.</p>
          </div>
        {:else}
          {#each followReeds.reeds as reed (reed.id)}
            <div class="reed-item" role="button" tabindex="0"
              on:click={() => goto(`/reed/${reed.userID}/${reed.id}`)}
              on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.userID}/${reed.id}`)}>
              <div class="reed-header">
                <ReedAuthorHeader
                  userID={reed.userID}
                  username={followReeds.authors[reed.userID]?.username ?? reed.userID}
                  nameTag="h3"
                  subtext={formatRelativeTime(reed.serverSignature.timestamp)}
                />
              </div>
              {#if reed.replying}
                <div class="quote-container">
                  <Quote reedRef={reed.replying} type="reply" missing={false} linked={true} />
                </div>
              {/if}
              {#if (reed.content || '').trim()}
                <div class="reed-preview">
                  <MarkdownParser text={reed.content} preview={true} />
                </div>
              {/if}
              {#if reed.echoing}
                <div class="quote-container">
                  <Quote reedRef={reed.echoing} type="echo" missing={false} linked={true} />
                </div>
              {/if}
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <BottomToolbar currentPage="feeds" />
  </div>
</Auth>

<style>
  .feed-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .feed-content-wrap {
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
    .feed-content-wrap {
      padding: 0.5rem;
    }

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
