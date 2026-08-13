<script>
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { serverConnection } from '$lib/services/serverConnection';
  import { broadcastReedQueue, removeBroadcastReed } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { formatRelativeTime } from '$lib/utils/time';
  import { isBlankEcho } from '$lib/utils/emptyEcho';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import FeedTabs from '$lib/components/FeedTabs.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import { captureWindowScroll, restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** @type {import('./$types').PageData} */
  export let data;

  const BROADCAST_KEY = 'broadcastReeds';
  const BROADCAST_LIMIT = 50;

  function saveBroadcastReed(reed, username, existing) {
    if (isBlankEcho(reed)) return existing;
    if (existing.reeds.some(r => r.id === reed.id)) return existing;
    const updatedReeds = [reed, ...existing.reeds].slice(0, BROADCAST_LIMIT);
    const updatedAuthors =
      username
        ? { ...existing.authors, [reed.userID]: { username } }
        : existing.authors;
    const updated = { reeds: updatedReeds, authors: updatedAuthors };
    sessionStorage.setItem(BROADCAST_KEY, JSON.stringify(updated));
    return updated;
  }

  let broadcastReeds = data.broadcastReeds;
  $: broadcastReeds = data.broadcastReeds;

  $: if ($broadcastReedQueue) {
    handleBroadcastReed($broadcastReedQueue.reed, $broadcastReedQueue.username);
  }

  async function handleBroadcastReed(reed, username) {
    if (isBlankEcho(reed)) return;
    if (reed?.userID && (await followingRepository.isFollowing(reed.userID))) {
      return;
    }
    broadcastReeds = saveBroadcastReed(reed, username, broadcastReeds);
  }

  // Drop session-broadcast reeds that already arrived via the follow feed.
  // Pre-follow broadcast reeds (not in followFeedIds) stay put.
  async function pruneBroadcastFollowedReeds(current) {
    let followIds = new Set();
    try {
      followIds = new Set(JSON.parse(sessionStorage.getItem('followFeedIds') ?? '[]'));
    } catch {
      // ignore
    }
    const kept = [];
    let changed = false;
    for (const reed of current.reeds) {
      if (followIds.has(reed.id)) {
        removeBroadcastReed(reed.id);
        changed = true;
        continue;
      }
      kept.push(reed);
    }
    return changed ? { reeds: kept, authors: current.authors } : current;
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
    broadcastReeds = await pruneBroadcastFollowedReeds(broadcastReeds);
    await serverConnection.connect();
    serverConnection.subscribeToBroadcast();
    if (pendingScrollY != null) {
      const y = pendingScrollY;
      pendingScrollY = null;
      await restoreWindowScroll(y);
    }
  });

  onDestroy(() => {
    serverConnection.unsubscribeFromBroadcast();
  });
</script>

<Auth>
  <div class="feed-container">
    <FeedTabs active="broadcast" />

    <div class="feed-content-wrap">
      <div class="reeds-list">
        {#if broadcastReeds.reeds.length === 0}
          <div class="waiting-state">
            <div class="waiting-pulse"></div>
            <p>Reeds from the network will load here...</p>
          </div>
        {:else}
          {#each broadcastReeds.reeds as reed (reed.id)}
            <div class="reed-item" role="button" tabindex="0"
              on:click={() => goto(`/reed/${reed.userID}/${reed.id}`)}
              on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.userID}/${reed.id}`)}>
              <div class="reed-header">
                <ReedAuthorHeader
                  userID={reed.userID}
                  serverID={reed.serverSignature?.serverID ?? ''}
                  username={broadcastReeds.authors[reed.userID]?.username ?? reed.userID}
                  nameTag="h3"
                  subtext={formatRelativeTime(reed.serverSignature.timestamp)}
                  stopPropagation
                />
              </div>
              {#if reed.replying}
                <div class="quote-container">
                  <Quote reedRef={reed.replying} type="reply" missing={false} linked={false} />
                </div>
              {/if}
              {#if (reed.content || '').trim()}
                <div class="reed-preview">
                  <MarkdownParser text={reed.content} preview={true} />
                </div>
              {/if}
              {#if reed.echoing}
                <div class="quote-container">
                  <Quote reedRef={reed.echoing} type="echo" missing={false} linked={false} />
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

  .waiting-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 1rem;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .waiting-pulse {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--primary);
    animation: pulse 1.5s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; transform: scale(1); }
    50% { opacity: 0.4; transform: scale(0.8); }
  }

  .waiting-state p {
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
