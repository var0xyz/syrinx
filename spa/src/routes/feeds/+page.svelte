<script>
  import { onMount, onDestroy } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { serverConnection } from '$lib/services/serverConnection';
  import {
    broadcastReedQueue,
    getFollowcastReeds,
    initFollowcastIds,
    profileReedQueue,
    newReedQueue,
    removeBroadcastReed,
    reedsService,
  } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { isBlankEcho } from '$lib/utils/emptyEcho';
  import { goto } from '$app/navigation';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Avatar from '$lib/components/Avatar.svelte';

  let user = null;
  let loading = true;
  let activeSection = 'followcast'; // 'broadcast' or 'followcast'

  const BROADCAST_KEY = 'broadcastReeds';
  const BROADCAST_LIMIT = 50;

  /** @type {Map<string, any>} echoing ref → original reed */
  let echoedReeds = new Map();
  /** @type {Map<string, any>} echoing ref → original author user */
  let echoedReedUsers = new Map();
  let pendingEchoRequests = new Set();
  let lastHandledProfileReedId = '';
  let lastHandledNewReedId = '';

  function loadBroadcastReeds() {
    const defaultValue = { reeds: [], authors: {} }
    if (!sessionStorage.getItem(BROADCAST_KEY)) {
      return defaultValue;
    }
    try {
      return JSON.parse(sessionStorage.getItem(BROADCAST_KEY));
    } catch {
      sessionStorage.removeItem(BROADCAST_KEY);
    }
    return defaultValue;
  }

  function saveBroadcastReed(reed, username, existing) {
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

  let broadcastReeds = { reeds: [], authors: {} };
  let followcastReeds = { reeds: [], authors: {} };

  $: if ($broadcastReedQueue) {
    handleBroadcastReed($broadcastReedQueue.reed, $broadcastReedQueue.username);
  }

  $: profileArrived = $profileReedQueue?.reed;
  $: if (profileArrived && profileArrived.id !== lastHandledProfileReedId) {
    lastHandledProfileReedId = profileArrived.id;
    void onProfileReedArrived(profileArrived);
  }

  $: newArrived = $newReedQueue?.reed;
  $: if (newArrived && newArrived.id !== lastHandledNewReedId) {
    lastHandledNewReedId = newArrived.id;
    void loadFollowcast();
  }

  async function onProfileReedArrived(arrived) {
    const all = [...broadcastReeds.reeds, ...followcastReeds.reeds];
    const echoRef = all.find((r) => {
      if (!isBlankEcho(r)) return false;
      const parsed = parseReedRef(r.echoing);
      return parsed && parsed.reedId === arrived.id && parsed.authorId === arrived.userID;
    })?.echoing;

    if (echoRef) {
      await mergeEchoOriginal(arrived, echoRef);
    }
  }

  async function mergeEchoOriginal(original, echoRefKey) {
    pendingEchoRequests.delete(echoRefKey);
    const echoMap = new Map(echoedReeds);
    echoMap.set(echoRefKey, original);
    echoedReeds = echoMap;

    try {
      const author = await userRepository.getByUserId(original.userID);
      if (author) {
        const userMap = new Map(echoedReedUsers);
        userMap.set(echoRefKey, author);
        echoedReedUsers = userMap;
      }
    } catch {
      // username falls back to userID in the template
    }
  }

  async function handleBroadcastReed(reed, username) {
    if (reed?.userID && (await followingRepository.isFollowing(reed.userID))) {
      return;
    }
    broadcastReeds = saveBroadcastReed(reed, username, broadcastReeds);
    await resolveBlankEchoes(broadcastReeds.reeds);
  }

  async function loadFollowcast() {
    followcastReeds = await getFollowcastReeds();
    await resolveBlankEchoes(followcastReeds.reeds);
  }

  /** Load originals for blank echoes; request missing ones and omit them from the list until they arrive. */
  async function resolveBlankEchoes(reeds) {
    const blank = reeds.filter(isBlankEcho);
    if (blank.length === 0) return;

    const echoMap = new Map(echoedReeds);
    const userMap = new Map(echoedReedUsers);

    await Promise.all(blank.map(async (reed) => {
      const key = reed.echoing;
      if (echoMap.has(key)) return;
      const parsed = parseReedRef(key);
      if (!parsed) return;
      const original = await reedsService.getReed(parsed.authorId, parsed.reedId);
      if (original) {
        pendingEchoRequests.delete(key);
        echoMap.set(key, original);
        try {
          const author = await userRepository.getByUserId(original.userID);
          if (author) userMap.set(key, author);
        } catch {
          // username falls back to userID in the template
        }
        return;
      }
      if (!pendingEchoRequests.has(key)) {
        pendingEchoRequests.add(key);
        serverConnection.requestReedContent(parsed.reedId, parsed.authorId, parsed.serverId);
      }
    }));

    echoedReeds = echoMap;
    echoedReedUsers = userMap;
  }

  function visibleFeedReeds(reeds) {
    return reeds.filter((r) => !isBlankEcho(r) || echoedReeds.has(r.echoing));
  }

  function feedDisplay(reed) {
    if (isBlankEcho(reed) && echoedReeds.has(reed.echoing)) {
      const displayReed = echoedReeds.get(reed.echoing);
      const displayUser = echoedReedUsers.get(reed.echoing);
      return {
        displayReed,
        username: displayUser?.username ?? displayReed.userID,
        href: `/reed/${displayReed.userID}/${displayReed.id}`,
      };
    }
    return null;
  }

  function setActiveSection(section) {
    activeSection = section;
    if (section === 'followcast') loadFollowcast();
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
    broadcastReeds = loadBroadcastReeds();

    try {
      user = await authService.getCurrentUser();
    } catch (error) {
      console.error('Error getting user:', error);
    } finally {
      loading = false;
    }

    await initFollowcastIds();
    broadcastReeds = await pruneBroadcastFollowedReeds(broadcastReeds);
    await resolveBlankEchoes(broadcastReeds.reeds);
    if (activeSection === 'followcast') loadFollowcast();

    await serverConnection.connect();
    serverConnection.subscribeToBroadcast();
  });

  onDestroy(() => {
    serverConnection.unsubscribeFromBroadcast();
  });
</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading feeds...</h2>
        <p>Please wait while we fetch your latest updates.</p>
      </div>
    </div>
  </div>
{:else}
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

          {#if visibleFeedReeds(broadcastReeds.reeds).length === 0}
            <div class="waiting-state">
              <div class="waiting-pulse"></div>
              <p>Listening for new reeds...</p>
            </div>
          {:else}
            {#each visibleFeedReeds(broadcastReeds.reeds) as reed (reed.id)}
              {@const swapped = feedDisplay(reed)}
              {@const displayReed = swapped?.displayReed ?? reed}
              {@const username = swapped?.username ?? (broadcastReeds.authors[reed.userID]?.username ?? reed.userID)}
              {@const href = swapped?.href ?? `/reed/${reed.userID}/${reed.id}`}
              <div class="feed-item" role="button" tabindex="0"
                on:click={() => goto(href)}
                on:keydown={(e) => e.key === 'Enter' && goto(href)}>
                <div class="feed-header">
                  <div class="feed-author">
                    <div class="avatar">
                      <Avatar
                        userID={displayReed.userID}
                        username={username}
                        size="40px"
                      />
                    </div>
                    <div class="author-info">
                      <span class="author-name">{username}</span>
                      <span class="feed-time">{formatRelativeTime(displayReed.serverSignature.timestamp)}</span>
                    </div>
                  </div>
                </div>
                {#if (displayReed.content || '').trim()}
                  <div class="feed-content">
                    <MarkdownParser text={displayReed.content} preview={true} />
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

          {#if visibleFeedReeds(followcastReeds.reeds).length === 0}
            {#if followcastReeds.reeds.length > 0}
              <div class="waiting-state">
                <div class="waiting-pulse"></div>
                <p>Loading echoed reeds...</p>
              </div>
            {:else}
              <div class="empty-state">
                <div class="empty-icon">👥</div>
                <h3>No reeds from people you follow yet.</h3>
                <p>Follow people and their reeds will appear here.</p>
              </div>
            {/if}
          {:else}
            {#each visibleFeedReeds(followcastReeds.reeds) as reed (reed.id)}
              {@const swapped = feedDisplay(reed)}
              {@const displayReed = swapped?.displayReed ?? reed}
              {@const username = swapped?.username ?? (followcastReeds.authors[reed.userID]?.username ?? reed.userID)}
              {@const href = swapped?.href ?? `/reed/${reed.userID}/${reed.id}`}
              <div class="feed-item" role="button" tabindex="0"
                on:click={() => goto(href)}
                on:keydown={(e) => e.key === 'Enter' && goto(href)}>
                <div class="feed-header">
                  <div class="feed-author">
                    <div class="avatar">
                      <Avatar
                        userID={displayReed.userID}
                        username={username}
                        size="40px"
                      />
                    </div>
                    <div class="author-info">
                      <span class="author-name">{username}</span>
                      <span class="feed-time">{formatRelativeTime(displayReed.serverSignature.timestamp)}</span>
                    </div>
                  </div>
                </div>
                {#if (displayReed.content || '').trim()}
                  <div class="feed-content">
                    <MarkdownParser text={displayReed.content} preview={true} />
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
{/if}

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


  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
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
