<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  /** Who the verification is about, named in the confirmation copy. */
  export let subjectUserID: string;
  export let busy = false;
  export let compact = false;

  const dispatch = createEventDispatcher();

  let confirming = false;

  function confirm() {
    confirming = false;
    dispatch('withdraw');
  }
</script>

{#if confirming}
  <div class="confirm" role="group" aria-label="Confirm withdrawal">
    <p class="warning">
      Withdraw your verification of {subjectUserID}? This is public and signed —
      anyone relying on it will see it is gone. You can only verify this key
      again after 24 hours.
    </p>
    <div class="actions">
      <button class="btn danger" disabled={busy} on:click={confirm}>
        {busy ? 'Withdrawing…' : 'Yes, withdraw'}
      </button>
      <button class="btn secondary" disabled={busy} on:click={() => (confirming = false)}>
        Cancel
      </button>
    </div>
  </div>
{:else}
  <button
    class="btn danger"
    class:compact
    disabled={busy}
    on:click={() => (confirming = true)}
  >
    Withdraw
  </button>
{/if}

<style>
  .confirm {
    border: 1px solid var(--error, #e03131);
    border-radius: 6px;
    padding: 0.6rem 0.75rem;
    margin: 0.4rem 0;
  }

  .warning {
    font-size: 0.8rem;
    line-height: 1.5;
    margin: 0 0 0.5rem;
    color: var(--fg);
  }

  .actions {
    display: flex;
    gap: 0.5rem;
  }

  .btn {
    padding: 0.35rem 0.8rem;
    border-radius: 6px;
    border: 1px solid transparent;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .btn.compact {
    padding: 0.2rem 0.6rem;
    font-size: 0.75rem;
  }

  .btn.danger {
    background: var(--error, #e03131);
    color: #fff;
  }

  .btn.danger:hover:not(:disabled) {
    opacity: 0.9;
  }

  .btn.secondary {
    background: var(--input-bg);
    color: var(--fg);
    border-color: var(--border);
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
