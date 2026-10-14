<script lang="ts">
  import { onDestroy } from 'svelte';

  export let ariaLabel: string = 'Copy to clipboard';
  /** Does the copy. Resolving to false, or throwing, skips the checkmark. */
  export let copy: () => Promise<boolean | void>;

  const CONFIRM_MS = 1500;
  let copied = false;
  let timer: ReturnType<typeof setTimeout> | null = null;

  async function handleClick(): Promise<void> {
    let ok: boolean;
    try {
      ok = (await copy()) !== false;
    } catch {
      ok = false;
    }
    if (!ok) return;
    copied = true;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => (copied = false), CONFIRM_MS);
  }

  onDestroy(() => {
    if (timer) clearTimeout(timer);
  });
</script>

<button type="button" class="copy-btn" class:copied on:click={handleClick} aria-label={copied ? 'Copied' : ariaLabel}>
  {#if copied}
    <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="20 6 9 17 4 12"></polyline>
    </svg>
  {:else}
    <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
    </svg>
  {/if}
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

  .copy-btn.copied,
  .copy-btn.copied:hover {
    color: var(--success);
  }
</style>
