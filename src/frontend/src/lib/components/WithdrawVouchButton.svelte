<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';

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

<button
  class="btn danger"
  class:compact
  disabled={busy}
  on:click={() => (confirming = true)}
>
  {busy ? 'Withdrawing…' : 'Withdraw'}
</button>

{#if confirming}
  <ConfirmDialog
    title="Withdraw this verification?"
    message={`Your verification of ${subjectUserID} will be withdrawn. You can only verify this user again after 24 hours.`}
    confirmLabel="Yes, withdraw"
    on:confirm={confirm}
    on:cancel={() => (confirming = false)}
  />
{/if}

<style>
  .btn {
    padding: 0.35rem 0.8rem;
    border-radius: 6px;
    border: 1px solid transparent;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    background: var(--error, #e03131);
    color: #fff;
  }

  .btn.compact {
    padding: 0.2rem 0.6rem;
    font-size: 0.75rem;
  }

  .btn:hover:not(:disabled) {
    opacity: 0.9;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
