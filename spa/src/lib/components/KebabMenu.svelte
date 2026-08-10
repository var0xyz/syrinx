<script lang="ts">
  /**
   * @typedef {Object} KebabMenuOption
   * @property {string} label
   * @property {() => void} onSelect
   * @property {boolean} [danger]
   */

  /** @type {KebabMenuOption[]} */
  export let options = [];
  export let ariaLabel = 'More options';

  let open = false;
  /** @type {HTMLDivElement} */
  let container;

  function toggle() {
    open = !open;
  }

  function close() {
    open = false;
  }

  function select(option) {
    close();
    option.onSelect();
  }

  function handleWindowClick(event) {
    if (open && container && !container.contains(event.target)) {
      close();
    }
  }

  function handleKeydown(event) {
    if (event.key === 'Escape') close();
  }
</script>

<svelte:window on:click={handleWindowClick} on:keydown={handleKeydown} />

<div class="kebab-menu" bind:this={container} on:click|stopPropagation role="presentation">
  <button
    class="kebab-trigger"
    on:click|stopPropagation={toggle}
    aria-label={ariaLabel}
    aria-haspopup="true"
    aria-expanded={open}
  >
    <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
      <circle cx="12" cy="5" r="2"></circle>
      <circle cx="12" cy="12" r="2"></circle>
      <circle cx="12" cy="19" r="2"></circle>
    </svg>
  </button>

  {#if open}
    <div class="kebab-dropdown" role="menu">
      {#each options as option}
        <button
          class="kebab-option"
          class:danger={option.danger}
          role="menuitem"
          on:click|stopPropagation={() => select(option)}
        >
          {option.label}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .kebab-menu {
    position: relative;
  }

  .kebab-trigger {
    background: transparent;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--muted);
    border-radius: 4px;
    width: auto;
  }

  .kebab-trigger:hover {
    color: var(--fg);
    background: var(--input-bg);
  }

  .kebab-dropdown {
    position: absolute;
    top: calc(100% + 0.25rem);
    right: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    min-width: 10rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    overflow: hidden;
    padding: 0.25rem;
  }

  .kebab-option {
    background: none;
    border: none;
    text-align: left;
    padding: 0.5rem 0.75rem;
    font-size: 0.9rem;
    color: var(--fg);
    cursor: pointer;
    border-radius: 4px;
    white-space: nowrap;
  }

  .kebab-option:hover {
    background: var(--input-bg);
  }

  .kebab-option.danger {
    color: var(--error);
  }
</style>
