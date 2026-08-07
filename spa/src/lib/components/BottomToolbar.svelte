<script>
  import { page } from '$app/stores';

  export let currentPage = 'reeds';

  $: showMesh =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
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
  <a href="/invites" class="toolbar-btn" class:active={currentPage === 'invites'}>
    <span class="icon">✉️</span>
    <span class="label">Invites</span>
  </a>
  {#if showMesh}
    <a href="/mesh" class="toolbar-btn" class:active={currentPage === 'mesh'}>
      <span class="icon">🌐</span>
      <span class="label">Mesh</span>
    </a>
  {/if}
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
