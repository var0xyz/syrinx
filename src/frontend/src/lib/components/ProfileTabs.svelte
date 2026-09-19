<script>
  /** @type {'profile' | 'liked' | 'mentions'} */
  export let active;
</script>

<div class="section-toggle">
  <a href="/reeds" class="toggle-btn" class:active={active === 'profile'}>
    👤 Profile
  </a>
  <a href="/reeds/likes" class="toggle-btn" class:active={active === 'liked'}>
    💛 Liked
  </a>
  <a href="/reeds/mentions" class="toggle-btn" class:active={active === 'mentions'}>
    📣 Mentions
  </a>
</div>

<style>
  .section-toggle {
    display: flex;
    flex-wrap: wrap;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0.5rem;
    gap: 0.25rem;
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .toggle-btn {
    flex: 1 1 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: 1px solid var(--border);
    color: var(--muted);
    padding: 0.25rem 0.75rem;
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.2s ease;
    font-size: 0.9rem;
    font-weight: 500;
    text-decoration: none;
    white-space: nowrap;
  }

  .toggle-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
    border-color: var(--primary);
  }

  .toggle-btn.active {
    background: var(--primary);
    color: var(--button-text);
    border-color: var(--primary);
  }
</style>
