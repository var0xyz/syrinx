<script>
  import { onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { pipeReedQueue } from '$lib/repositories/reeds';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';

  /** @type {import('./$types').PageData} */
  export let data;

  let tag = data.tag;
  let reeds = data.reeds;
  let authors = data.authors;
  let lastHandledPipeReedId = '';
  let subscribedTag = '';

  $: tag = data.tag;
  $: reeds = data.reeds;
  $: authors = data.authors;

  $: if (tag && tag !== subscribedTag) {
    void switchPipeSubscription(tag);
  }

  $: pipeArrived = $pipeReedQueue?.reed;
  $: if (pipeArrived && pipeArrived.id !== lastHandledPipeReedId) {
    lastHandledPipeReedId = pipeArrived.id;
    void onLiveReed(pipeArrived, $pipeReedQueue?.username);
  }

  async function onLiveReed(reed, username) {
    if (!reed?.tags?.includes(tag)) return;
    if (reeds.some((r) => r.id === reed.id)) return;

    let nextAuthors = authors;
    if (username || !authors[reed.userID]) {
      const existing = authors[reed.userID];
      const fromRepo = existing ?? (await userRepository.getByUserId(reed.userID).catch(() => null));
      nextAuthors = {
        ...authors,
        [reed.userID]: fromRepo ?? {
          id: reed.userID,
          username: username ?? reed.userID,
        },
      };
      authors = nextAuthors;
    }

    reeds = [reed, ...reeds];
  }

  async function switchPipeSubscription(nextTag) {
    const prev = subscribedTag;
    subscribedTag = nextTag;
    await serverConnection.connect();
    if (prev && prev !== nextTag) {
      serverConnection.unsubscribePipe(prev);
    }
    await serverConnection.subscribePipe(nextTag);
  }

  onDestroy(() => {
    if (subscribedTag) {
      serverConnection.unsubscribePipe(subscribedTag);
      subscribedTag = '';
    }
  });
</script>

<Auth>
  <div class="pipe-container">
    <div class="pipe-header">
      <p class="pipe-sub">Pipe of reeds with tag: #{tag}</p>
    </div>

    <div class="pipe-content">
      <div class="pipe-list">
        {#if reeds.length === 0}
          <div class="waiting-state">
            <div class="waiting-pulse"></div>
            <p>No local reeds for #{tag} yet. Listening…</p>
          </div>
        {:else}
          {#each reeds as reed (reed.id)}
            <div
              class="feed-item"
              role="button"
              tabindex="0"
              on:click={() => goto(`/reed/${reed.userID}/${reed.id}`)}
              on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.userID}/${reed.id}`)}
            >
              <div class="feed-header">
                <div class="feed-author">
                  <div class="avatar">
                    <Avatar
                      userID={reed.userID}
                      username={authors[reed.userID]?.username ?? reed.userID}
                      size="40px"
                    />
                  </div>
                  <div class="author-info">
                    <span class="author-name">{authors[reed.userID]?.username ?? reed.userID}</span>
                    <span class="feed-time">{formatRelativeTime(reed.serverSignature?.timestamp)}</span>
                  </div>
                </div>
              </div>
              {#if (reed.content || '').trim()}
                <div class="feed-content">
                  <MarkdownParser text={reed.content} preview={true} />
                </div>
              {/if}
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <BottomToolbar currentPage="" />
  </div>
</Auth>

<style>
  .pipe-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .pipe-header {
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1.25rem 1rem 0.5rem;
  }

  .pipe-sub {
    margin: 0.35rem 0 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .pipe-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .pipe-list {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .waiting-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
    padding: 3rem 1rem;
    color: var(--muted);
    text-align: center;
  }

  .waiting-pulse {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--primary);
    animation: pulse 1.4s ease-in-out infinite;
  }

  @keyframes pulse {
    0%,
    100% {
      opacity: 0.35;
      transform: scale(0.9);
    }
    50% {
      opacity: 1;
      transform: scale(1.1);
    }
  }

  .feed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
    cursor: pointer;
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
    color: var(--fg);
  }

  @media (max-width: 768px) {
    .pipe-header,
    .pipe-content {
      padding-left: 0.5rem;
      padding-right: 0.5rem;
    }
  }
</style>
