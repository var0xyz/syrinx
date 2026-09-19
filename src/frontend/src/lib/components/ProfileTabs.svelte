<script>
  /** @type {'profile' | 'liked' | 'mentions'} */
  export let active;
</script>

<div class="section-toggle">
  <a href="/reeds" class="toggle-btn" class:active={active === 'profile'}>
    Profile
  </a>
  <a href="/reeds/likes" class="toggle-btn" class:active={active === 'liked'}>
    Liked
  </a>
  <a href="/reeds/mentions" class="toggle-btn" class:active={active === 'mentions'}>
    Mentions
  </a>
</div>

<style>
  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0 0.5rem;
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .toggle-btn {
    flex: 1 1 0;
    text-align: center;
    background: transparent;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--muted);
    padding: 0.7rem 0.5rem;
    cursor: pointer;
    font-size: 0.85rem;
    font-weight: 600;
    text-decoration: none;
    white-space: nowrap;
  }

  .toggle-btn:hover {
    color: var(--fg);
  }

  .toggle-btn.active {
    color: var(--fg);
    border-color: var(--primary);
  }
</style>
