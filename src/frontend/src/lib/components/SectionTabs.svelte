<script>
  /** @type {{ href: string, label: string }[]} */
  export let tabs;

  /** @type {string} */
  export let active;

  const keyOf = (label) => label.toLowerCase().replace(/\s+/g, '-');
</script>

<div class="section-toggle">
  <div class="section-toggle-inner">
    {#each tabs as tab (tab.href)}
      <a href={tab.href} class="toggle-btn" class:active={active === keyOf(tab.label)}>
        {tab.label}
      </a>
    {/each}
  </div>
</div>

<style>
  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: calc(3rem + 1px);
    z-index: 10;
  }

  .section-toggle-inner {
    display: flex;
    flex: 1;
    padding: 0 0.5rem;
  }

  @media (min-width: 768px) {
    .section-toggle {
      display: none;
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
