<script lang="ts">
  export let ariaLabel: string = 'Show QR code';
</script>

<button class="qr-btn" on:click aria-label={ariaLabel}>
  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <rect x="3" y="3" width="7" height="7"></rect>
    <rect x="14" y="3" width="7" height="7"></rect>
    <rect x="3" y="14" width="7" height="7"></rect>
    <line x1="14" y1="14" x2="14" y2="14.01"></line>
    <line x1="17.5" y1="14" x2="17.5" y2="17.5"></line>
    <line x1="21" y1="14" x2="21" y2="14.01"></line>
    <line x1="14" y1="17.5" x2="17.5" y2="17.5"></line>
    <line x1="14" y1="21" x2="17.5" y2="21"></line>
    <line x1="21" y1="17.5" x2="21" y2="21"></line>
  </svg>
</button>

<style>
  .qr-btn {
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

  .qr-btn:hover {
    color: var(--fg);
    background: var(--input-bg);
  }
</style>
