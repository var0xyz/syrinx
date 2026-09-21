<script>
  import { getContext, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { pipeReedQueue } from '$lib/repositories/reeds';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';
  import { pipesRepository } from '$lib/repositories/pipes';

  const { refresh: refreshPipes } = getContext('pipes-panel');

  /** @type {import('./$types').PageData} */
  export let data;

  let tag = data.tag;
  let reeds = data.reeds;
  let authors = data.authors;
  let lastHandledPipeReedId = '';
  let subscribedTag = '';
  let pinned = false;

  $: tag = data.tag;
  $: reeds = data.reeds;
  $: authors = data.authors;

  $: if (tag && tag !== subscribedTag) {
    void switchPipeSubscription(tag);
  }

  $: if (tag) {
    void pipesRepository.isPinned(tag).then((v) => (pinned = v));
  }

  async function togglePin() {
    if (pinned) {
      await pipesRepository.unpin(tag);
    } else {
      await pipesRepository.pin(tag);
    }
    pinned = !pinned;
    await refreshPipes();
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

<div class="pipe-header">
  <h2 class="pipe-sub">#{tag}</h2>
  <button
    class="pin-btn"
    class:unpin-btn={pinned}
    on:click={togglePin}
    aria-label={pinned ? 'Unpin pipe' : 'Pin pipe'}
  >
    <span class="action-icon" class:pin-icon={!pinned} class:unpin-icon={pinned}></span>
  </button>
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
          on:click={() => goto(`/reed/${reed.id}`)}
          on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${reed.id}`)}
        >
          <div class="feed-header">
            <ReedAuthorHeader
              userID={reed.userID}
              username={authors[reed.userID]?.username ?? reed.userID}
              subtext={formatRelativeTime(reed.serverSignature?.timestamp)}
              stopPropagation
              linked={false}
            />
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

<style>
  .pipe-header {
    display: flex;
    justify-content: space-between;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1.25rem 1rem 0.5rem;
  }

  .pipe-header p {
    margin: 0;
  }

  .pipe-sub {
    margin: 0;
    color: var(--fg);
    font-weight: 600;
    display: inline;
  }

  .pin-btn {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.35rem;
    border-radius: 6px;
    color: var(--muted);
    width: auto;
  }

  .pin-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .pin-btn.unpin-btn {
    color: var(--error);
  }

  .pin-btn.unpin-btn:hover {
    background: var(--input-bg);
    color: var(--error);
  }

  .action-icon {
    display: inline-block;
    width: 1.1rem;
    height: 1.1rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .pin-icon {
    -webkit-mask-image: url('/icons/pin-24.png');
    mask-image: url('/icons/pin-24.png');
  }

  .unpin-icon {
    -webkit-mask-image: url('/icons/unpin-24.png');
    mask-image: url('/icons/unpin-24.png');
  }

  .pipe-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 0.5rem;
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

  .feed-content {
    padding: 1rem;
    color: var(--fg);
  }

  @media (max-width: 768px) {
    .pipe-header {
      padding: 0.5rem 1.5rem 0;
    }

    .pipe-content {
      padding: 0.5rem;
    }
  }
</style>
