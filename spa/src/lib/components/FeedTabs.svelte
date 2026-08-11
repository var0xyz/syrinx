<script>
  /** @type {'follow' | 'broadcast' | 'liked'} */
  export let active;
</script>

<div class="section-toggle">
  <a href="/feed/follow" class="toggle-btn" class:active={active === 'follow'}>
    👥 Follow
  </a>
  <a href="/feed/broadcast" class="toggle-btn" class:active={active === 'broadcast'}>
    📡 Broadcast
  </a>
  <a href="/feed/liked" class="toggle-btn" class:active={active === 'liked'}>
    ❤️ Likes
  </a>
</div>

<style>
  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0.5rem;
    gap: 0.25rem;
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .toggle-btn {
    flex: 1;
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
