<script>
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { serverConnection } from '$lib/services/serverConnection';
  import {
    broadcastReedQueue,
    getFollowcastReeds,
    followReedQueue,
    removeBroadcastReed,
  } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { formatRelativeTime } from '$lib/utils/time';
  import { isBlankEcho } from '$lib/utils/emptyEcho';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import { captureWindowScroll, restoreWindowScroll } from '$lib/utils/scrollSnapshot';

  /** @type {import('./$types').PageData} */
  export let data;

  let user = data.user;

  /** @type {number | null} */
  let pendingScrollY = null;

  /** @type {import('./$types').Snapshot<number>} */
  export const snapshot = {
    capture: () => captureWindowScroll(),
    restore: (y) => {
      pendingScrollY = y;
    },
  };

  /** @param {string} hash */
  function sectionFromHash(hash) {
    const h = (hash || '').replace(/^#/, '').toLowerCase();
    return h === 'broadcast' ? 'broadcast' : 'followcast';
  }

  /** @type {'broadcast' | 'followcast'} */
  let activeSection = sectionFromHash(typeof window !== 'undefined' ? window.location.hash : '');

  const BROADCAST_KEY = 'broadcastReeds';
  const BROADCAST_LIMIT = 50;

  let lastHandledFollowReedId = '';

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
  let followcastReeds = data.followcastReeds;

  $: user = data.user;
  $: broadcastReeds = data.broadcastReeds;
  $: followcastReeds = data.followcastReeds;

  // Keep tab in sync with /feeds#followcast | /feeds#broadcast (back/forward too).
  $: {
    const fromHash = sectionFromHash($page.url.hash);
    if (fromHash !== activeSection) {
      activeSection = fromHash;
      if (fromHash === 'followcast') void loadFollowcast();
    }
  }

  $: if ($broadcastReedQueue) {
    handleBroadcastReed($broadcastReedQueue.reed, $broadcastReedQueue.username);
  }

  $: followArrived = $followReedQueue?.reed;
  $: if (followArrived && followArrived.id !== lastHandledFollowReedId) {
    lastHandledFollowReedId = followArrived.id;
    void loadFollowcast();
  }

  async function handleBroadcastReed(reed, username) {
    if (isBlankEcho(reed)) return;
    if (reed?.userID && (await followingRepository.isFollowing(reed.userID))) {
      return;
    }
    broadcastReeds = saveBroadcastReed(reed, username, broadcastReeds);
  }

  async function loadFollowcast() {
    followcastReeds = await getFollowcastReeds();
  }

  function setActiveSection(section) {
    const hash = section === 'broadcast' ? '#broadcast' : '#followcast';
    if (sectionFromHash($page.url.hash) !== section) {
      void goto(`/feeds${hash}`, { replaceState: true, noScroll: true, keepFocus: true });
    } else if (section === 'followcast') {
      void loadFollowcast();
    }
  }

  // Drop session-broadcast reeds that already arrived via followcast.
  // Pre-follow broadcast reeds (not in followcastIds) stay put.
  async function pruneBroadcastFollowedReeds(current) {
    let followIds = new Set();
    try {
      followIds = new Set(JSON.parse(sessionStorage.getItem('followcastIds') ?? '[]'));
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

  onMount(async () => {
    // Normalize bare /feeds → /feeds#followcast so the tab is always in the URL.
    if (!$page.url.hash) {
      void goto('/feeds#followcast', { replaceState: true, noScroll: true, keepFocus: true });
    }
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
    <div class="feeds-container">
    <!-- Section Toggle -->
    <div class="section-toggle">
      <button
        class="toggle-btn"
        class:active={activeSection === 'followcast'}
        on:click={() => setActiveSection('followcast')}
      >
      <!--
        Followcast: You only get messages from the people you follow, but they
        get automatically persisted. This also means that you will broadcast
        them to whoever requests them too.
      -->
        👥 Followcast
      </button>
      <button
        class="toggle-btn"
        class:active={activeSection === 'broadcast'}
        on:click={() => setActiveSection('broadcast')}
      >
      <!--
        Broadcast: You get everything that is posted on the platform, but it
        doesn't get persisted to IndexedDB, only to SessionStorage (that is,
        until you click on it).
      -->
        📡 Broadcast
      </button>
    </div>

    <!-- Main Content -->
    <div class="feeds-content">
      <div class="feeds-list">
        {#if activeSection === 'broadcast'}
          <!-- Broadcast Section -->
          <div class="section-header">
            <p>Community-wide reeds</p>
          </div>

          {#if broadcastReeds.reeds.length === 0}
            <div class="waiting-state">
              <div class="waiting-pulse"></div>
              <p>Listening for new reeds...</p>
            </div>
          {:else}
            {#each broadcastReeds.reeds as reed (reed.id)}
              <div class="feed-item" role="button" tabindex="0"
                on:click={() => goto(`/reed/${reed.userID}/${reed.id}`)}
                on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.userID}/${reed.id}`)}>
                <div class="feed-header">
                  <div class="feed-author">
                    <div class="avatar">
                      <Avatar
                        userID={reed.userID}
                        username={broadcastReeds.authors[reed.userID]?.username ?? reed.userID}
                        size="40px"
                      />
                    </div>
                    <div class="author-info">
                      <span class="author-name">{broadcastReeds.authors[reed.userID]?.username ?? reed.userID}</span>
                      <span class="feed-time">{formatRelativeTime(reed.serverSignature.timestamp)}</span>
                    </div>
                  </div>
                </div>
                {#if reed.replying}
                  <div class="feed-quote">
                    <Quote reedRef={reed.replying} type="reply" linked={false} />
                  </div>
                {/if}
                {#if (reed.content || '').trim()}
                  <div class="feed-content">
                    <MarkdownParser text={reed.content} preview={true} />
                  </div>
                {/if}
                {#if reed.echoing}
                  <div class="feed-quote">
                    <Quote reedRef={reed.echoing} type="echo" linked={false} />
                  </div>
                {/if}
              </div>
            {/each}
          {/if}
        {:else}
          <!-- Followcast Section -->
          <div class="section-header">
            <p>Reeds from people you follow</p>
          </div>

          {#if followcastReeds.reeds.length === 0}
            <div class="empty-state">
              <div class="empty-icon">👥</div>
              <h3>No reeds from people you follow yet.</h3>
              <p>Follow people and their reeds will appear here.</p>
            </div>
          {:else}
            {#each followcastReeds.reeds as reed (reed.id)}
              <div class="feed-item" role="button" tabindex="0"
                on:click={() => goto(`/reed/${reed.userID}/${reed.id}`)}
                on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.userID}/${reed.id}`)}>
                <div class="feed-header">
                  <div class="feed-author">
                    <div class="avatar">
                      <Avatar
                        userID={reed.userID}
                        username={followcastReeds.authors[reed.userID]?.username ?? reed.userID}
                        size="40px"
                      />
                    </div>
                    <div class="author-info">
                      <span class="author-name">{followcastReeds.authors[reed.userID]?.username ?? reed.userID}</span>
                      <span class="feed-time">{formatRelativeTime(reed.serverSignature.timestamp)}</span>
                    </div>
                  </div>
                </div>
                {#if reed.replying}
                  <div class="feed-quote">
                    <Quote reedRef={reed.replying} type="reply" linked={true} />
                  </div>
                {/if}
                {#if (reed.content || '').trim()}
                  <div class="feed-content">
                    <MarkdownParser text={reed.content} preview={true} />
                  </div>
                {/if}
                {#if reed.echoing}
                  <div class="feed-quote">
                    <Quote reedRef={reed.echoing} type="echo" linked={true} />
                  </div>
                {/if}
              </div>
            {/each}
          {/if}
        {/if}
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="feeds" />
    </div>
  </Auth>

<style>
  .feeds-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0.5rem;
    gap: 0.25rem;
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .toggle-btn {
    flex: 1;
    background: transparent;
    border: 1px solid var(--border);
    color: var(--muted);
    padding: 0.75rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.2s ease;
    font-size: 0.9rem;
    font-weight: 500;
  }

  .toggle-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
    border-color: var(--primary);
  }

  .toggle-btn.active {
    background: var(--primary);
    color: var(--button-text);
    border-color: var(--primary);
  }

  .section-header {
    padding: 1rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }

  .section-header p {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .feeds-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .feeds-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .feed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
  }

  .feed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .feed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .feed-author {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .avatar {
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .author-info {
    display: flex;
    flex-direction: column;
  }

  .author-name {
    font-weight: 600;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .feed-time {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .feed-content {
    padding: 1rem;
  }

  .feed-quote {
    padding: 0 1rem 0.75rem;
  }

  .feed-quote:first-of-type {
    padding-top: 0.75rem;
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

  /* Responsive Design */
  @media (max-width: 768px) {
    .feeds-content {
      padding: 0.5rem;
    }

    .feed-header {
      padding: 0.75rem;
    }

    .feed-content {
      padding: 0.75rem;
    }

    .feeds-list {
      gap: .5rem;
    }
  }
</style>
