<script lang="ts">
  import { pendingChatCount } from '$lib/stores/chat';

  export let currentPage = 'reeds';
</script>

<nav class="bottom-toolbar">
  <a href="/reeds" class="toolbar-btn" class:active={currentPage === 'reeds'}>
    <span class="icon">🌾</span>
    <span class="label">Reeds</span>
  </a>
  <a href="/feeds" class="toolbar-btn" class:active={currentPage === 'feeds'}>
    <span class="icon">📰</span>
    <span class="label">Feed</span>
  </a>
  <a href="/chats" class="toolbar-btn" class:active={currentPage === 'chats'}>
    <span class="icon-wrap">
      <span class="icon">💬</span>
      {#if $pendingChatCount > 0}
        <span class="badge">{$pendingChatCount}</span>
      {/if}
    </span>
    <span class="label">Chats</span>
  </a>
  <a href="/profile" class="toolbar-btn" class:active={currentPage === 'profile'}>
    <span class="icon">👤</span>
    <span class="label">Profile</span>
  </a>
</nav>

<style>
  .bottom-toolbar {
    display: flex;
    background: var(--surface);
    border-top: 1px solid var(--border);
    gap: 0.25rem;
    position: sticky;
    bottom: 0;
    z-index: 100;
    border-radius: 0.5rem 0.5rem 0 0;
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
  }

  .toolbar-btn .icon {
    font-size: 1.2rem;
    margin-bottom: 0.25rem;
  }

  .toolbar-btn .label {
    font-size: 0.75rem;
    font-weight: 500;
  }

  .icon-wrap {
    position: relative;
    display: inline-flex;
    margin-bottom: 0.25rem;
  }

  .icon-wrap .icon {
    margin-bottom: 0;
  }

  .badge {
    position: absolute;
    top: -6px;
    right: -10px;
    background: #e53e3e;
    color: #fff;
    font-size: 0.65rem;
    font-weight: 700;
    min-width: 16px;
    height: 16px;
    border-radius: 99px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 3px;
  }

  @media (max-width: 768px) {
    .toolbar-btn .label {
      font-size: 0.7rem;
    }

    .toolbar-btn {
      padding: 0.5rem 0.25rem;
    }
  }

  @media (max-width: 480px) {
    .toolbar-btn .icon {
      font-size: 1rem;
    }
  }
</style>
