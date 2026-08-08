<script>
  import Avatar from '$lib/components/Avatar.svelte';

  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let username;
  /** Secondary line under the name — caller formats (relative time, "Pending…", etc). */
  export let subtext = '';
  /** Extra class on the subtext span, e.g. for a "Pending…" state style. */
  export let subtextClass = '';
  export let avatarSize = '40px';
  /** Wrapping element for the name — 'h3' for a page's primary list (a11y heading), 'span' for secondary lists. */
  export let nameTag = 'span';
</script>

<div class="reed-author-header">
  <div class="reed-author-avatar">
    <Avatar {userID} {username} size={avatarSize} />
  </div>
  <div class="reed-author-info">
    <svelte:element this={nameTag} class="reed-author-name">{username}</svelte:element>
    {#if subtext}
      <span class="reed-author-subtext {subtextClass}">{subtext}</span>
    {/if}
  </div>
</div>

<style>
  .reed-author-header {
    display: flex;
    gap: 0.75rem;
    min-width: 0;
  }

  .reed-author-avatar {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .reed-author-info {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    min-width: 0;
  }

  .reed-author-name {
    font-weight: 600;
    color: var(--fg);
    font-size: 1rem;
    word-break: break-word;
    padding: 0;
    margin: 0;
  }

  .reed-author-subtext {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .reed-author-subtext.pending {
    color: var(--primary);
    font-style: italic;
  }
</style>
