<script>
  // One ripple's rendered row — extracted out of RipplesSection so the
  // same markup can be reused for both the main (settled) list and the
  // held-while-composer-open pending list, without duplicating it.
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { createEventDispatcher } from 'svelte';

  /** @type {import('$lib/types/api').Ripple} */
  export let ripple;
  /** Resolved username for ripple.userID, or null (removed account). */
  export let username = /** @type {string | null} */ (null);
  /** Resolved username for ripple.replyingTo's author, if that target is
   * loaded — null if the target isn't loaded, undefined if there's no
   * replyingTo at all. */
  export let replyingToUsername = /** @type {string | null | undefined} */ (undefined);
  /** Whether replyingTo resolves to a loaded ripple at all (vs. an
   * out-of-page/unloaded target, rendered as generic "a comment"). */
  export let replyingToLoaded = false;
  export let ownUserID = '';
  /** True during the ripples-expired burn animation — see RipplesSection's
   * `burning`. Only ever true for rows in the settled list; pending rows
   * are held out of `ripples` and can't be burning. */
  export let burning = false;
  /** This row's staggered delay into the shared burn animation, in ms —
   * meaningless when `burning` is false. */
  export let burnDelayMs = 0;
  /** False for a row rendered in the pending (composer-still-open) list —
   * it isn't part of `ripples` yet, so replyingToHashes-driven inline
   * composer rendering (only wired up for the settled list) can't target
   * it. Hides the "reply" action there rather than offering an action
   * that would silently do nothing visible. */
  export let replyable = true;

  const dispatch = createEventDispatcher();
</script>

<li
  class="ripple-row"
  class:ripple-row--reply={!!ripple.replyingTo}
  class:ripple-row--burning={burning}
  style={burning ? `--burn-delay: ${burnDelayMs}ms` : undefined}
>
  <div class="ripple-avatar">
    <Avatar userID={ripple.userID} username={username ?? ''} size="32px" />
  </div>
  <div class="ripple-body">
    <p class="ripple-meta">
      {#if ripple.userID === ownUserID && !ripple.deleted}
        <button type="button" class="ripple-delete-btn" on:click={() => dispatch('delete', ripple.hash)} aria-label="Delete ripple">
          <span class="ripple-delete-icon"></span>
        </button>
      {/if}
      <span class="ripple-meta-text">
        {#if username}
          <Username userID={ripple.userID} {username} color="var(--muted)" />
        {:else}
          <span class="ripple-username-removed">[removed account]</span>
        {/if}
        · {formatRelativeTime(ripple.postedAt)}
      </span>
    </p>
    {#if ripple.replyingTo}
      <p class="ripple-reply-chip">
        replying to {#if replyingToLoaded}
          {#if replyingToUsername}
            @{replyingToUsername}
          {:else}
            a removed account
          {/if}
        {:else}
          a comment
        {/if}
      </p>
    {/if}
    {#if ripple.deleted}
      <p class="ripple-content ripple-content-deleted">[DELETED]</p>
    {:else}
      <p class="ripple-content">
        {ripple.content}
        {#if replyable}
          <button type="button" class="ripple-action ripple-reply-inline" on:click={() => dispatch('reply')}>reply</button>
        {/if}
      </p>
    {/if}
  </div>
</li>

<style>
  .ripple-row {
    display: flex;
    gap: 0.5rem;
    padding: 0.25rem 0;
  }

  /* Not a full nested-reply indent (00's lock: flat rendering) — just a
     visual hint that this response is part of a thread, not top-level. */
  .ripple-row--reply {
    margin-left: 1rem;
  }

  /* Fades/rises away in its own delayed pass (--burn-delay, set inline
     per-row) so the whole list doesn't vanish as one flat block — closer
     to embers catching one after another. */
  .ripple-row--burning {
    animation: ripple-burn 0.8s ease-in forwards;
    animation-delay: var(--burn-delay, 0ms);
  }

  @keyframes ripple-burn {
    0% {
      opacity: 1;
      filter: none;
      transform: translateY(0) scale(1);
    }
    30% {
      opacity: 1;
      filter: brightness(1.6) saturate(1.8) hue-rotate(-25deg);
      transform: translateY(-2px) scale(1.01);
    }
    100% {
      opacity: 0;
      filter: brightness(1.6) saturate(1.8) hue-rotate(-25deg) blur(2px);
      transform: translateY(-14px) scale(0.97);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .ripple-row--burning {
      animation: ripple-burn-reduced 0.3s ease-in forwards;
      animation-delay: 0ms;
    }
  }

  @keyframes ripple-burn-reduced {
    from { opacity: 1; }
    to { opacity: 0; }
  }

  .ripple-avatar {
    flex: 0 0 auto;
    padding-top: 0.1rem;
  }

  .ripple-body {
    min-width: 0;
    flex: 1 1 auto;
  }

  .ripple-meta {
    display: flex;
    align-items: baseline;
    margin: 0;
    font-size: 0.82rem;
  }

  .ripple-meta-text {
    color: var(--muted);
    min-width: 0;
  }

  .ripple-username-removed {
    font-style: italic;
  }

  .ripple-delete-btn {
    display: inline-flex;
    flex: 0 0 auto;
    width: auto;
    align-items: center;
    background: none;
    border: none;
    padding: 0;
    margin: 0 0.4rem 0 0;
    line-height: 0;
    cursor: pointer;
    color: var(--muted);
    opacity: 0.7;
  }

  .ripple-delete-btn:hover {
    opacity: 1;
    color: #d9534f;
  }

  .ripple-delete-icon {
    display: inline-block;
    width: 0.85rem;
    height: 0.85rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/trash-16.png');
    mask-image: url('/icons/trash-16.png');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .ripple-reply-chip {
    margin: 0 0 0.25rem;
    font-size: 0.78rem;
    color: var(--muted);
    font-style: italic;
  }

  .ripple-content {
    margin: 0 0 0.3rem;
    font-size: 0.88rem;
    line-height: 1.45;
    color: var(--fg);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .ripple-content-deleted {
    margin: 0 0 0.3rem;
    font-size: 0.88rem;
    line-height: 1.45;
    white-space: pre-wrap;
    color: var(--muted);
    font-style: italic;
  }

  .ripple-action {
    display: inline;
    width: auto;
    background: none;
    border: none;
    padding: 0;
    margin: 0;
    font-size: 0.75rem;
    color: var(--muted);
    text-align: left;
    cursor: pointer;
  }

  .ripple-action:hover {
    color: var(--fg);
    text-decoration: underline;
  }

  .ripple-reply-inline {
    margin-left: 0.5rem;
    white-space: nowrap;
  }
</style>
