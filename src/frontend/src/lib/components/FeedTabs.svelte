<script>
  /** @type {'follow' | 'broadcast' | 'list'} */
  export let active;
</script>

<div class="section-toggle">
  <div class="section-toggle-inner">
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
</div>

<style>
  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .section-toggle-inner {
    display: flex;
    flex: 1;
    padding: 0 1rem;
  }

  @media (min-width: 768px) {
    .section-toggle-inner {
      flex-grow: 0;
      min-width: 680px;
      margin: 0 auto;
    }
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
