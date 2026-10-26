<script>
  import { page } from '$app/stores';
  import { unreadInteractions } from '$lib/stores/unreadInteractions';
  import { townHalls } from './data.js';

  // Mock copy of BottomToolbar with a Town halls item; pages that use it must
  // not also mount the real BottomToolbar, so the layout's instance stays hidden.
  export let currentPage = '';

  $: hasUnreadInteractions = $unreadInteractions.replies || $unreadInteractions.ripples || $unreadInteractions.mentions;
  $: hasLive = $townHalls.some((t) => t.state === 'live');

  $: isAdmin =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
</script>

  <nav class="bottom-toolbar">
    <a
      href="/reeds"
      class="toolbar-btn"
      class:active={currentPage === 'reeds'}
    >
      <span class="icon">🌾</span>
      <span class="label">Reeds</span>
    </a>
    <a
      href="/feed"
      class="toolbar-btn"
      class:active={currentPage === 'feeds'}
    >
      <span class="icon">📰</span>
      <span class="label">Feeds</span>
    </a>
    <a
      href="/feed/lists"
      class="toolbar-btn"
      class:active={currentPage === 'lists'}
    >
      <span class="icon">📋</span>
      <span class="label">Lists</span>
    </a>
    <a
      href="/replies"
      class="toolbar-btn"
      class:active={currentPage === 'interactions'}
    >
      <span class="icon-wrap">
        <span class="icon">💬</span>
        {#if hasUnreadInteractions}<span class="toolbar-unread-dot"></span>{/if}
      </span>
      <span class="label">Interactions</span>
    </a>
    <a
      href="/mocks/townhalls/list"
      class="toolbar-btn"
      class:active={currentPage === 'townhalls'}
    >
      <span class="icon-wrap">
        <span class="icon">🎙️</span>
        {#if hasLive}<span class="toolbar-live-dot"></span>{/if}
      </span>
      <span class="label">Town halls</span>
    </a>
    <a
      href="/search"
      class="toolbar-btn"
      class:active={currentPage === 'search'}
    >
      <span class="icon">🔍</span>
      <span class="label">Search</span>
    </a>
    {#if isAdmin}
      <a
        href="/network"
        class="toolbar-btn"
        class:active={currentPage === 'network'}
      >
        <span class="icon">🌐</span>
        <span class="label">Network</span>
      </a>
    {:else}
      <a
        href="/invites"
        class="toolbar-btn"
        class:active={currentPage === 'invites'}
      >
        <span class="icon">✉️</span>
        <span class="label">Invites</span>
      </a>
    {/if}
    <a
      href="/account"
      class="toolbar-btn"
      class:active={currentPage === 'account'}
    >
      <span class="icon">👤</span>
      <span class="label">Account</span>
    </a>
  </nav>

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

  .toolbar-live-dot {
    position: absolute;
    top: 0;
    right: -4px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--error);
    border: 1.5px solid var(--surface);
    animation: live-pulse 1.6s ease-in-out infinite;
  }

  @keyframes live-pulse {
    50% {
      opacity: 0.35;
    }
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
