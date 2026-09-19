<script>
  /** @type {'follow' | 'broadcast' | 'list'} */
  export let active;
</script>

<div class="section-toggle">
  <a href="/feed/follow" class="toggle-btn" class:active={active === 'follow'}>
    Follow
  </a>
  <a href="/feed/broadcast" class="toggle-btn" class:active={active === 'broadcast'}>
    Broadcast
  </a>
  <a href="/feed/lists" class="toggle-btn" class:active={active === 'list'}>
    Lists
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
