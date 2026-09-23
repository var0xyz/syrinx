<script>
  import { onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { currentToolbarPage, toolbarUsers } from '$lib/stores/bottomToolbar';
  import { unreadInteractions } from '$lib/stores/unreadInteractions';

  $: hasUnreadInteractions = $unreadInteractions.replies || $unreadInteractions.mentions;

  // Legacy per-page usage: <BottomToolbar currentPage="x" /> sets which tab
  // is active on the single toolbar instance (rendered once in the root
  // layout) instead of rendering a toolbar of its own — so the toolbar's
  // DOM, and its scroll position, survive navigation between pages.
  export let currentPage = undefined;

  $: if (currentPage !== undefined) currentToolbarPage.set(currentPage);

  if (currentPage !== undefined) {
    toolbarUsers.update((n) => n + 1);
    onDestroy(() => toolbarUsers.update((n) => n - 1));
  }

  $: isAdmin =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
</script>

{#if currentPage === undefined && $toolbarUsers > 0}
  <nav class="bottom-toolbar">
    <a
      href="/reeds"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'reeds'}
    >
      <span class="icon">🌾</span>
      <span class="label">Reeds</span>
    </a>
    <a
      href="/feed"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'feeds'}
    >
      <span class="icon">📰</span>
      <span class="label">Feeds</span>
    </a>
    <a
      href="/feed/lists"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'lists'}
    >
      <span class="icon">📋</span>
      <span class="label">Lists</span>
    </a>
    <a
      href="/replies"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'interactions'}
    >
      <span class="icon-wrap">
        <span class="icon">💬</span>
        {#if hasUnreadInteractions}<span class="toolbar-unread-dot"></span>{/if}
      </span>
      <span class="label">Interactions</span>
    </a>
    <a
      href="/search/reeds"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'search'}
    >
      <span class="icon">🔍</span>
      <span class="label">Search</span>
    </a>
    {#if isAdmin}
      <a
        href="/network"
        class="toolbar-btn"
        class:active={$currentToolbarPage === 'network'}
      >
        <span class="icon">🌐</span>
        <span class="label">Network</span>
      </a>
    {:else}
      <a
        href="/invites"
        class="toolbar-btn"
        class:active={$currentToolbarPage === 'invites'}
      >
        <span class="icon">✉️</span>
        <span class="label">Invites</span>
      </a>
    {/if}
    <a
      href="/account"
      class="toolbar-btn"
      class:active={$currentToolbarPage === 'account'}
    >
      <span class="icon">👤</span>
      <span class="label">Account</span>
    </a>
  </nav>
{/if}

<style>
  .bottom-toolbar {
    display: flex;
    background: var(--surface);
    border-top: 1px solid var(--border);
    position: sticky;
    bottom: 0;
    z-index: 100;
    border-radius: 0.5rem 0.5rem 0 0;
    padding-bottom: env(safe-area-inset-bottom);
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: none;
    -ms-overflow-style: none;
  }

  .bottom-toolbar::-webkit-scrollbar {
    display: none;
  }

  @media (min-width: 768px) {
    .bottom-toolbar {
      display: none;
    }
  }

  .toolbar-btn {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.75rem 0.5rem;
    text-decoration: none;
    transition: all 0.2s ease;
    color: var(--muted);
    background: transparent;
  }

  .toolbar-btn:first-of-type {
    border-radius: 0.5rem 0 0 0;
  }

  .toolbar-btn:last-of-type {
    border-radius: 0 0.5rem 0 0;
  }

  .toolbar-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .toolbar-btn.active {
    background: var(--primary);
    color: var(--button-text);
    border-radius: 0.5rem 0.5rem 0 0;
  }

  .toolbar-btn .icon-wrap {
    position: relative;
    display: inline-flex;
    margin-bottom: 0.25rem;
  }

  .toolbar-btn .icon {
    font-size: 1.2rem;
  }

  .toolbar-unread-dot {
    position: absolute;
    top: 0;
    right: -2px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--error);
    border: 1.5px solid var(--surface);
  }

  .toolbar-btn .label {
    font-size: 0.75rem;
    font-weight: 500;
  }

  /* Responsive Design */
  @media (max-width: 768px) {
    .toolbar-btn .label {
      font-size: 0.7rem;
    }

    .toolbar-btn {
      padding: 0.5rem 0;
      min-width: 5rem;
    }
  }

  @media (max-width: 480px) {
    .toolbar-btn .icon {
      font-size: 1rem;
    }
  }
</style>
