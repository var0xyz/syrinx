<script lang="ts">
  export let ariaLabel: string = 'Copy to clipboard';
</script>

<button class="copy-btn" on:click aria-label={ariaLabel}>
  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
  </svg>
</button>

<style>
  .copy-btn {
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

  .copy-btn:hover {
    color: var(--fg);
    background: var(--input-bg);
  }
</style>
