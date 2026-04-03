<script>
  import { onMount } from 'svelte';
  import { userRepository } from '$lib/repositories/user';
  import { stripMarkdown, formatRelativeTime } from '$lib/repositories/reeds';

  /** @type {import('$lib/types/reed').ReedType} */
  export let reed;

  /** @type {'echo' | 'reply'} */
  export let type = 'echo';

  /** @type {boolean} */
  export let missing = false;

  /** @type {number | undefined} */
  export let maxLines = 3;

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
      // Try to get username from cache first, then from API
      const user = await userRepository.getByUserId(reed.headers.author);
      username = user?.username || reed.headers.author;
    } catch (error) {
      console.error('Error fetching user:', error);
      username = reed.headers.author; // Fallback to user ID
    } finally {
      loading = false;
    }
  });
</script>

{#if missing}
  <div class="quote quote--missing" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Original reed unavailable</div>
  </div>
{:else if reed}
  <div class="quote" style="--border-color: {borderColor}; --max-lines: {maxLines}">
    {#if loading}
      <div class="quote-meta">{icon} Loading...</div>
    {:else}
      <div class="quote-meta">{icon} {label}{username} · {formatRelativeTime(reed.headers.timestamp)}</div>
      <div class="quote-content">{stripMarkdown(reed.content)}</div>
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

  .quote-meta {
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
  }

  .quote-content {
    font-size: 0.85rem;
    color: var(--fg);
    line-height: 1.4;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: var(--max-lines);
    -webkit-box-orient: vertical;
    white-space: pre-wrap;
  }

  .quote--missing .quote-meta {
    margin-bottom: 0;
    font-style: italic;
  }
</style>
