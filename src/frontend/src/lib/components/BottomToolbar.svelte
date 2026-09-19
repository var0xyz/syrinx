<script>
  import { page } from '$app/stores';

  export let currentPage = 'reeds';

  $: isAdmin =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
</script>

<nav class="bottom-toolbar">
  <a href="/reeds" class="toolbar-btn" class:active={currentPage === 'reeds'}>
    <span class="icon">🌾</span>
    <span class="label">Reeds</span>
  </a>
  <a href="/feed" class="toolbar-btn" class:active={currentPage === 'feeds'}>
    <span class="icon">📰</span>
    <span class="label">Feed</span>
  </a>
  {#if isAdmin}
    <a href="/network" class="toolbar-btn" class:active={currentPage === 'network'}>
      <span class="icon">🌐</span>
      <span class="label">Network</span>
    </a>
  {:else}
    <a href="/invites" class="toolbar-btn" class:active={currentPage === 'invites'}>
      <span class="icon">✉️</span>
      <span class="label">Invites</span>
    </a>
  {/if}
  <a href="/account" class="toolbar-btn" class:active={currentPage === 'account'}>
    <span class="icon">👤</span>
    <span class="label">Account</span>
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

  @media (min-width: 768px) {
    .bottom-toolbar {
      align-self: center;
      min-width: 680px;
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

  .toolbar-btn .icon {
    font-size: 1.2rem;
    margin-bottom: 0.25rem;
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
      padding: 0.5rem 0.25rem;
    }
  }

  @media (max-width: 480px) {
    .toolbar-btn .icon {
      font-size: 1rem;
    }
  }
</style>
