<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let url: string = '';
  export let open: boolean = false;

  const dispatch = createEventDispatcher();

  function confirm() {
    window.open(url, '_blank', 'noopener,noreferrer');
    dispatch('close');
  }

  function cancel() {
    dispatch('close');
  }
</script>

{#if open}
  <div class="overlay" on:click={cancel} role="presentation"></div>
  <div class="modal" role="dialog" aria-modal="true">
    <h3>⚠️ Attention</h3>
    <p class="description">You are being redirected to:</p>
    <p class="url">{url}</p>
    <div class="actions">
      <button class="btn btn-secondary" on:click={cancel}>Cancel</button>
      <button class="btn btn-primary" on:click={confirm}>Continue</button>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 2000;
  }

  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 2001;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    width: min(420px, calc(100vw - 2rem));
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .description {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .url {
    margin: 0;
    color: var(--fg);
    font-size: 0.85rem;
    word-break: break-all;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.5rem 0.75rem;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    transition: all 0.2s ease;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover { opacity: 0.9; }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover { background: var(--border); }
</style>
