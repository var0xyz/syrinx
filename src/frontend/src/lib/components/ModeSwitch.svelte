<script lang="ts">
  export let options: { value: string; label: string }[];
  export let value: string;
</script>

<div class="mode-switch" role="radiogroup">
  {#each options as option (option.value)}
    <button
      type="button"
      role="radio"
      aria-checked={value === option.value}
      class="mode-option"
      class:active={value === option.value}
      on:click={() => (value = option.value)}
    >
      {option.label}
    </button>
  {/each}
</div>

<style>
  .mode-switch {
    display: inline-flex;
    padding: 0.2rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 999px;
  }

  .mode-option {
    padding: 0.45rem 1.25rem;
    border: none;
    border-radius: 999px;
    background: transparent;
    color: var(--muted);
    font-size: 0.85rem;
    font-weight: 600;
    font-family: inherit;
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .mode-option:hover:not(.active) {
    color: var(--fg);
  }

  .mode-option.active {
    background: var(--primary);
    color: var(--button-text);
  }
</style>
