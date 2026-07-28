<script>
  import { onMount } from 'svelte';
  import { reedsService, unsignedReedsProcessed, profileReedQueue, newReedQueue } from '$lib/repositories/reeds';
  import { formatRelativeTime } from '$lib/utils/time';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import { userRepository } from '$lib/repositories/user';
  import { removeReedAsAuthor } from '$lib/services/reedRemoval';
  import { pendingRemovalSynced } from '$lib/repositories/pendingRemoval';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import { goto } from '$app/navigation';
  import { parseReedRef } from '$lib/utils/reedRef';

  export let authorId;
  export let isOwner = false;
  export let showWriteButton = false;

  let isWriteSectionOpen = false;
  let showNewReedBanner = false;
  let reeds = [];
  /** @type {import('$lib/types/reed').ReedType[]} */
  let pendingReeds = [];
  let profileUser = null;
  let loadingReeds = true;
  let errorLoadingReeds = '';
  let echoedReeds = new Map();
  let repliedToReeds = new Map();
  let echoedReedUsers = new Map();

  $: if ($unsignedReedsProcessed > 0) loadReeds();
  $: if ($pendingRemovalSynced > 0) loadReeds();
  // profileReedQueue carries explicitly requested content (profile subscription / REQUEST_REED).
  // We reload immediately only when the user is already at the top (no scroll loss risk).
  // Otherwise we show a banner instead, letting the user decide when to reload.
  // newReedQueue carries catch-up and follow-broadcast reeds — we never auto-reload for those,
  // since the user didn't ask for them and reloading would interrupt their current position.
  $: if ($profileReedQueue?.reed.userID === authorId) {
    if (window.scrollY === 0) {
      loadReeds();
    } else {
      showNewReedBanner = true;
    }
  }
  $: if ($newReedQueue?.reed.userID === authorId) {
    showNewReedBanner = true;
  }

  onMount(async () => {
    profileUser = await userRepository.getByUserId(authorId).catch(() => null);
    await loadReeds();
  });

  function* inverse() {
    let x = reeds.length - 1;
    while (x >= 0) {
      yield reeds[x];
      x--;
    }
  }

  async function loadReeds() {
    try {
      loadingReeds = true;
      errorLoadingReeds = '';
      reeds = await reedsService.getReedsByAuthor(authorId);
      pendingReeds = isOwner
        ? await reedsService.getUnsignedReedsByAuthor(authorId)
        : [];

      const allForQuotes = [...pendingReeds, ...reeds];

      // Fetch echoed reeds in parallel
      const echoEntries = allForQuotes
        .filter(r => r.echoing)
        .map(r => {
          const parsed = parseReedRef(r.echoing);
          if (!parsed) return null;
          return { key: r.echoing, author: parsed.authorId, reedId: parsed.reedId };
        })
        .filter(Boolean);

      const echoResults = await Promise.allSettled(
        echoEntries.map(({ author, reedId }) => reedsService.getReed(author, reedId))
      );

      const echoMap = new Map();
      echoEntries.forEach(({ key }, i) => {
        if (echoResults[i].status === 'fulfilled' && echoResults[i].value) {
          echoMap.set(key, echoResults[i].value);
        }
      });
      echoedReeds = echoMap;

      // Fetch authors for empty-echo swaps
      const emptyEchoKeys = allForQuotes
        .filter(r => r.echoing && !(r.content || '').trim() && echoMap.has(r.echoing))
        .map(r => r.echoing);

      const uniqueEchoAuthors = [...new Set(emptyEchoKeys.map(key => echoMap.get(key).userID))];
      const echoAuthorResults = await Promise.allSettled(
        uniqueEchoAuthors.map(author => userRepository.getByUserId(author))
      );
      const echoAuthorMap = new Map();
      uniqueEchoAuthors.forEach((author, i) => {
        if (echoAuthorResults[i].status === 'fulfilled' && echoAuthorResults[i].value) {
          echoAuthorMap.set(author, echoAuthorResults[i].value);
        }
      });
      const userMap = new Map();
      emptyEchoKeys.forEach(key => {
        const original = echoMap.get(key);
        if (original) userMap.set(key, echoAuthorMap.get(original.userID));
      });
      echoedReedUsers = userMap;

      // Fetch replied-to reeds in parallel
      const replyEntries = allForQuotes
        .filter(r => r.replying)
        .map(r => {
          const parsed = parseReedRef(r.replying);
          if (!parsed) return null;
          return { key: r.replying, author: parsed.authorId, reedId: parsed.reedId };
        })
        .filter(Boolean);

      const replyResults = await Promise.allSettled(
        replyEntries.map(({ author, reedId }) => reedsService.getReed(author, reedId))
      );

      const replyMap = new Map();
      replyEntries.forEach(({ key }, i) => {
        if (replyResults[i].status === 'fulfilled' && replyResults[i].value) {
          replyMap.set(key, replyResults[i].value);
        }
      });
      repliedToReeds = replyMap;
    } catch (error) {
      console.error('Error loading reeds:', error);
      errorLoadingReeds = 'Failed to load reeds';
    } finally {
      loadingReeds = false;
    }
  }

  async function deleteReed(reedId, pending = false) {
    if (!confirm('Are you sure you want to delete this reed?')) {
      return;
    }

    try {
      if (pending) {
        await reedsService.discardUnsignedReed(reedId);
        pendingReeds = pendingReeds.filter((reed) => reed.id !== reedId);
      } else {
        await removeReedAsAuthor(authorId, reedId);
        reeds = reeds.filter(reed => reed.id !== reedId);
      }
    } catch (error) {
      console.error('Error deleting reed:', error);
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.userID}/${reed.id}`);
  }
</script>

{#if showWriteButton}
  <button class="floating-write-btn" on:click={() => (isWriteSectionOpen = true)}>
    <span class="icon">✍️</span>
  </button>
  <NewReedModal open={isWriteSectionOpen} on:close={() => (isWriteSectionOpen = false)} />
{/if}

{#if showNewReedBanner}
  <div class="new-reed-banner">
    <div class="new-reed-msg">New reed available</div>
    <button on:click={() => { showNewReedBanner = false; loadReeds(); }}>Show</button>
    <button class="dismiss" on:click={() => (showNewReedBanner = false)}>✕</button>
  </div>
{/if}

<div class="reeds-list">
  {#if loadingReeds}
    <div class="loading">
      <h2>Loading reeds...</h2>
      <p>Please wait while we fetch your reeds.</p>
    </div>
  {:else if errorLoadingReeds}
    <div class="error-state">
      <div class="error-icon">⚠️</div>
      <h3>Error loading reeds</h3>
      <p>{errorLoadingReeds}</p>
      <button class="btn btn-primary" on:click={loadReeds}>Try Again</button>
    </div>
  {:else if reeds.length === 0 && pendingReeds.length === 0}
    <div class="empty-state">
      <div class="empty-icon">🌾</div>
      <h3>No reeds yet</h3>
      <p>Your reeds will appear here when you publish them.</p>
    </div>
  {:else}
    {#each pendingReeds as reed (reed.id)}
      {@const isEmptyEcho = !!(reed.echoing && !(reed.content || '').trim() && echoedReeds.has(reed.echoing))}
      {@const displayReed = isEmptyEcho ? echoedReeds.get(reed.echoing) : reed}
      {@const displayUser = isEmptyEcho ? (echoedReedUsers.get(reed.echoing) || { username: displayReed.userID }) : (profileUser || { username: authorId })}
      <div class="reed-item pending" role="button" tabindex="0" on:click={() => navigateToReed(reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(reed)}>
        <div class="reed-header">
          <div class="reed-info">
            <div class="reed-avatar">
              <Avatar userID={displayReed.userID} username={displayUser.username} size="40px" />
            </div>
            <div class="reed-details">
              <h3>{displayUser.username}</h3>
              <p class="pending-label">Pending…</p>
            </div>
          </div>
          {#if isOwner}
            <div class="reed-meta">
              <button class="reed-menu" on:click|stopPropagation={() => deleteReed(reed.id, true)} aria-label="Delete">🗑️</button>
            </div>
          {/if}
        </div>
        {#if !isEmptyEcho && reed.replying}
          <div class="quote-container">
            <Quote
              reed={repliedToReeds.get(reed.replying)}
              type="reply"
              missing={!repliedToReeds.has(reed.replying)}
              linked={false}
            />
          </div>
        {/if}
        {#if (displayReed.content || "").trim()}
          <div class={["reed-preview", !isEmptyEcho && reed.echoing && "echo", !isEmptyEcho && reed.replying && "reply"]}>
            <MarkdownParser text={displayReed.content} preview={true} />
          </div>
        {/if}
        {#if !isEmptyEcho && reed.echoing}
          <div class="quote-container">
            <Quote
              reed={echoedReeds.get(reed.echoing)}
              type="echo"
              missing={!echoedReeds.has(reed.echoing)}
              linked={false}
            />
          </div>
        {/if}
      </div>
    {/each}
    {#each inverse(reeds) as reed (reed.id)}
      {@const isEmptyEcho = !!(reed.echoing && !(reed.content || '').trim() && echoedReeds.has(reed.echoing))}
      {@const displayReed = isEmptyEcho ? echoedReeds.get(reed.echoing) : reed}
      {@const displayUser = isEmptyEcho ? (echoedReedUsers.get(reed.echoing) || { username: displayReed.userID }) : (profileUser || { username: authorId })}
      <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(displayReed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(displayReed)}>
        <div class="reed-header">
          <div class="reed-info">
            <div class="reed-avatar">
              <Avatar userID={displayReed.userID} username={displayUser.username} size="40px" />
            </div>
            <div class="reed-details">
              <h3>{displayUser.username}</h3>
              <p>{formatRelativeTime(displayReed.serverSignature.timestamp)}</p>
            </div>
          </div>
          {#if isOwner}
            <div class="reed-meta">
              <button class="reed-menu" on:click|stopPropagation={() => deleteReed(reed.id)} aria-label="Delete">🗑️</button>
            </div>
          {/if}
        </div>
        {#if !isEmptyEcho && reed.replying}
          <div class="quote-container">
            <Quote
              reed={repliedToReeds.get(reed.replying)}
              type="reply"
              missing={!repliedToReeds.has(reed.replying)}
              linked={false}
            />
          </div>
        {/if}
        {#if (displayReed.content || "").trim()}
          <div class={["reed-preview", !isEmptyEcho && reed.echoing && "echo", !isEmptyEcho && reed.replying && "reply"]}>
            <MarkdownParser text={displayReed.content} preview={true} />
          </div>
        {/if}
        {#if !isEmptyEcho && reed.echoing}
          <div class="quote-container">
            <Quote
              reed={echoedReeds.get(reed.echoing)}
              type="echo"
              missing={!echoedReeds.has(reed.echoing)}
              linked={false}
            />
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

  .floating-write-btn {
    position: fixed;
    bottom: 80px;
    right: 20px;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
  }

  .floating-write-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-write-btn .icon {
    font-size: 1.5rem;
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
  }

  .reed-info {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .reed-avatar {
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .reed-menu {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    border-radius: 4px;
    transition: background-color 0.2s ease;
  }

  .reed-menu:hover {
    background: var(--border);
  }

  .error-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .error-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .error-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .error-state p {
    margin: 0 0 1rem 0;
    font-size: 0.9rem;
  }

  .reed-details h3 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1rem;
    font-weight: 600;
  }

  .reed-details p {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .reed-details .pending-label {
    color: var(--primary);
    font-style: italic;
  }

  .reed-meta {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
  }

  .reed-preview.reply {
    padding-top: 0;
    padding-left: 2rem;
  }

  .reed-preview.echo {
    padding-bottom: 0;
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

  .loading p {
    margin: 0;
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

    .reed-preview.reply {
      padding-top: 0;
      padding-left: 1.5rem;
    }

    .quote-container {
      margin: 0.75rem;
    }
  }
</style>
