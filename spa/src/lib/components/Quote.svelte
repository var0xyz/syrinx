<script>
  import { goto } from '$app/navigation';
  import { reedsService } from '$lib/repositories/reeds';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { resolveBlankEchoChain } from '$lib/utils/emptyEcho';
  import MarkdownParser from './MarkdownParser.svelte';

  /** @type {import('$lib/types/reed').ReedType | null | undefined} */
  export let reed = null;

  /** When `reed` is null, fetch by this `userID@serverID/reedID` ref. */
  export let reedRef = '';

  /** @type {'echo' | 'reply'} */
  export let type = 'echo';

  /** @type {boolean} */
  export let missing = false;

  /** @type {boolean} */
  export let linked = false;

  /** Nesting depth — caps recursive quotes. */
  export let depth = 0;

  /** Optional preview line clamp (composer). */
  export let maxLines = 0;

  const MAX_DEPTH = 6;

  let username = '';
  let loading = true;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let displayReed = null;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let nestedReply = null;
  let nestedReplyMissing = false;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let nestedEcho = null;
  let nestedEchoMissing = false;
  let loadFailed = false;

  $: icon = type === 'echo' ? '📢' : '💬';
  $: label = type === 'reply' ? 'Replying to ' : '';
  $: borderColor = type === 'echo' ? 'var(--primary)' : '#7c3aed';
  $: reedId = reed?.id ?? '';
  $: void load(reedId, reedRef, missing, depth);

  async function load(_id, ref, isMissing, nestDepth) {
    loading = true;
    loadFailed = false;
    displayReed = null;
    nestedReply = null;
    nestedReplyMissing = false;
    nestedEcho = null;
    nestedEchoMissing = false;
    username = '';

    if (isMissing) {
      loading = false;
      return;
    }

    try {
      let source = reed ?? null;
      if (!source && ref) {
        const parsed = parseReedRef(ref);
        if (!parsed) {
          loadFailed = true;
          loading = false;
          return;
        }
        source = await reedsService.getReed(parsed.authorId, parsed.reedId);
        if (!source) {
          loadFailed = true;
          loading = false;
          return;
        }
      }
      if (!source) {
        loadFailed = true;
        loading = false;
        return;
      }

      const resolved = await resolveBlankEchoChain(source, (authorId, reedId) =>
        reedsService.getReed(authorId, reedId)
      );
      displayReed = resolved;

      try {
        const user = await userRepository.getByUserId(resolved.userID);
        username = user?.username || resolved.userID;
      } catch {
        username = resolved.userID;
      }

      if (nestDepth < MAX_DEPTH) {
        if (resolved.replying) {
          const parsed = parseReedRef(resolved.replying);
          if (parsed) {
            nestedReply = await reedsService.getReed(parsed.authorId, parsed.reedId);
            nestedReplyMissing = !nestedReply;
          } else {
            nestedReplyMissing = true;
          }
        }
        if (resolved.echoing) {
          const parsed = parseReedRef(resolved.echoing);
          if (parsed) {
            nestedEcho = await reedsService.getReed(parsed.authorId, parsed.reedId);
            nestedEchoMissing = !nestedEcho;
          } else {
            nestedEchoMissing = true;
          }
        }
      }
    } catch (error) {
      console.error('Error loading quote:', error);
      if (reed) {
        displayReed = reed;
        username = reed.userID;
      } else {
        loadFailed = true;
      }
    } finally {
      loading = false;
    }
  }

  function handleClick(event) {
    if (!linked || !displayReed) return;
    event.stopPropagation();
    goto(`/reed/${displayReed.userID}/${displayReed.id}`);
  }
</script>

{#if missing || loadFailed}
  <div class="quote quote--missing" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Original reed unavailable</div>
  </div>
{:else if loading}
  <div class="quote" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Loading...</div>
  </div>
{:else if displayReed}
  <div
    class="quote"
    class:quote--linked={linked}
    class:quote--clamped={maxLines > 0}
    style="--border-color: {borderColor}; --max-lines: {maxLines}"
    on:click={handleClick}
    role={linked ? 'link' : 'presentation'}
    tabindex={linked ? 0 : undefined}
    on:keydown={(e) => e.key === 'Enter' && handleClick(e)}
  >
    <div class="quote-meta">{icon} {label}{username}{#if displayReed.serverSignature?.timestamp} · {formatRelativeTime(displayReed.serverSignature.timestamp)}{/if}</div>

    {#if nestedReply || nestedReplyMissing}
      <div class="quote-nest">
        <svelte:self
          reed={nestedReply}
          type="reply"
          missing={nestedReplyMissing}
          linked={linked}
          depth={depth + 1}
        />
      </div>
    {/if}

    {#if (displayReed.content || '').trim()}
      <MarkdownParser text={displayReed.content} preview={true} className="quote-content" />
    {/if}

    {#if nestedEcho || nestedEchoMissing}
      <div class="quote-nest">
        <svelte:self
          reed={nestedEcho}
          type="echo"
          missing={nestedEchoMissing}
          linked={linked}
          depth={depth + 1}
        />
      </div>
    {/if}
  </div>
{/if}

<style>
  .quote {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-left: 3px solid var(--border-color);
    border-radius: 8px;
    background: var(--bg);
  }

  .quote--linked {
    cursor: pointer;
  }

  .quote--linked:hover {
    background: var(--input-bg);
  }

  .quote--clamped :global(.quote-content) {
    display: -webkit-box;
    -webkit-line-clamp: var(--max-lines);
    line-clamp: var(--max-lines);
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .quote-meta {
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
  }

  .quote-nest {
    margin: 0.4rem 0;
  }

  :global(.quote-content) {
    font-size: 0.85rem;
    color: var(--fg);
    line-height: 1.4;
    white-space: pre-wrap;
  }

  .quote--missing .quote-meta {
    margin-bottom: 0;
    font-style: italic;
  }
</style>
