<script>
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { isBlankEcho, resolveBlankEchoFromMap } from '$lib/utils/emptyEcho';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import ReedActionsMenu from '$lib/components/ReedActionsMenu.svelte';
  import ThreadContext from '$lib/components/ThreadContext.svelte';

  export let reed;
  export let authorId;
  export let isOwner = false;
  export let pinned = false;
  export let profileUser = null;
  export let echoedReeds = new Map();
  export let repliedToReeds = new Map();
  export let echoedReedUsers = new Map();
  export let onNavigate = () => {};
  export let onTogglePin = () => {};
  export let onDelete = () => {};

  $: displayReed = resolveBlankEchoFromMap(reed, echoedReeds);
  $: isUnwrapped = isBlankEcho(reed) && displayReed.id !== reed.id;
  $: awaitingOriginal =
    isBlankEcho(reed) &&
    isBlankEcho(displayReed) &&
    !(displayReed.echoing && echoedReeds.has(displayReed.echoing));
  $: shareTarget = awaitingOriginal ? reed : displayReed;
  // A thread is deleted whole, from its head; a later part can't be.
  $: deleteOption = reed.thread
    ? reed.thread.index === 0
      ? [{ label: 'Delete thread', danger: true, icon: '/icons/trash-16.svg', onSelect: () => onDelete(reed.id, false, true) }]
      : []
    : [{ label: 'Delete', danger: true, icon: '/icons/trash-16.svg', onSelect: () => onDelete(reed.id) }];
  $: displayUser = isUnwrapped ? (echoedReedUsers.get(displayReed.userID) || { username: displayReed.userID }) : (profileUser || { username: authorId });
</script>

<div class="reed-item" role="button" tabindex="0" on:click={() => onNavigate(awaitingOriginal ? reed : displayReed)} on:keydown={(e) => e.key === 'Enter' && onNavigate(awaitingOriginal ? reed : displayReed)}>
  <div class="reed-header">
    <ReedAuthorHeader
      userID={displayReed.userID}
      username={displayUser.username}
      nameTag="h3"
      subtext={formatRelativeTime(fromUnix((awaitingOriginal ? reed : displayReed).serverSignature?.signedAt ?? reed.serverSignature?.signedAt))}
      stopPropagation
      linked={false}
    />
    <div class="reed-meta">
      {#if pinned}
        <span class="pin-badge" title="Pinned" aria-hidden="true">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M16 3l5 5-5 5v5l-4-4-6 6-1-1 6-6-4-4h5l5-5z"/></svg>
        </span>
      {/if}
      <ReedActionsMenu
        reedRef={shareTarget.serverSignature ? shareTarget.id : ''}
        userID={shareTarget.userID}
        username={displayUser.username}
        content={shareTarget.content}
        showProfile={false}
        extraOptions={isOwner ? [
          pinned
            ? { label: 'Unpin', icon: '/icons/pin-16-filled.svg', onSelect: () => onTogglePin(reed) }
            : { label: 'Pin', icon: '/icons/pin-16-outlined.svg', onSelect: () => onTogglePin(reed) },
          ...deleteOption,
        ] : []}
      />
    </div>
  </div>
  <ThreadContext reed={displayReed} where="before" />
  {#if !awaitingOriginal && displayReed.replying}
    <div class="quote-container">
      <Quote
        reed={repliedToReeds.get(displayReed.replying.to)}
        reedRef={displayReed.replying.to}
        type="reply"
        missing={false}
        linked={false}
      />
    </div>
  {/if}
  {#if !awaitingOriginal && (displayReed.content || "").trim()}
    <div class={["reed-preview", !isUnwrapped && reed.echoing && "echo", !isUnwrapped && reed.replying && "reply"]}>
      <ThreadContext reed={displayReed} where="label" />
      <MarkdownParser text={displayReed.content} preview={true} />
    </div>
  {/if}
  <ThreadContext reed={displayReed} where="after" />
  {#if displayReed.echoing}
    <div class="quote-container">
      <Quote
        reed={echoedReeds.get(displayReed.echoing)}
        reedRef={displayReed.echoing}
        type="echo"
        missing={false}
        linked={false}
      />
    </div>
  {/if}
</div>

<style>
  .reed-item {
    /* No overflow: hidden — the kebab dropdown is an absolutely
       positioned child that must be able to escape this card's bounds. */
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
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

  .pin-badge {
    color: var(--text-secondary);
    display: inline-flex;
    align-items: center;
  }

  .reed-meta {
    display: flex;
    align-items: center;
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

  @media (max-width: 768px) {
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
