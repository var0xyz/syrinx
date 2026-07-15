<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import MarkdownParser from './MarkdownParser.svelte';

  /** @type {import('$lib/types/reed').ReedType} */
  export let reed;

  /** @type {'echo' | 'reply'} */
  export let type = 'echo';

  /** @type {boolean} */
  export let missing = false;

  /** @type {boolean} */
  export let linked = false;

  let username = '';
  let loading = true;

  $: icon = type === 'echo' ? '📢' : '💬';
  $: label = type === 'echo' ? '' : 'Replying to ';
  $: borderColor = type === 'echo' ? 'var(--primary)' : '#7c3aed';

  onMount(async () => {
    if (missing || !reed) {
      loading = false;
      return;
    }

    try {
      const user = await userRepository.getByUserId(reed.headers.author);
      username = user?.username || reed.headers.author;
    } catch (error) {
      console.error('Error fetching user:', error);
      username = reed.headers.author;
    } finally {
      loading = false;
    }
  });

  function handleClick(event) {
    if (!linked || !reed) return;
    event.stopPropagation();
    goto(`/reed/${reed.headers.author}/${reed.headers.id}`);
  }
</script>

{#if missing}
  <div class="quote quote--missing" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Original reed unavailable</div>
  </div>
{:else if reed}
  <div
    class="quote"
    class:quote--linked={linked}
    style="--border-color: {borderColor}"
    on:click={handleClick}
    role={linked ? 'link' : 'presentation'}
    tabindex={linked ? 0 : undefined}
    on:keydown={(e) => e.key === 'Enter' && handleClick(e)}
  >
    {#if loading}
      <div class="quote-meta">{icon} Loading...</div>
    {:else}
      <div class="quote-meta">{icon} {label}{username} · {formatRelativeTime(reed.server.timestamp)}</div>
      <MarkdownParser text={reed.content} preview={true} className="quote-content" />
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

  .quote-meta {
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
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
