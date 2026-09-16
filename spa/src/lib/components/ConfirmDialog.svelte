<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let title: string;
  export let message: string;
  export let confirmLabel = 'Delete';

  const dispatch = createEventDispatcher();

  function confirm() {
    dispatch('confirm');
  }

  function cancel() {
    dispatch('cancel');
  }
</script>

<div class="overlay" on:click={cancel} role="presentation"></div>
<div class="modal" role="dialog" aria-modal="true" aria-labelledby="confirm-dialog-title">
  <h3 id="confirm-dialog-title">{title}</h3>
  <p>{message}</p>

  <div class="actions">
    <button class="btn btn-secondary" on:click={cancel}>Cancel</button>
    <button class="btn btn-danger" on:click={confirm}>{confirmLabel}</button>
  </div>
</div>

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
    width: min(380px, calc(100vw - 2rem));
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  p {
    margin: 0;
    font-size: 0.9rem;
    line-height: 1.5;
    color: var(--fg);
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

  .btn-danger {
    background: var(--error);
    color: #fff;
  }

  .btn-danger:hover {
    opacity: 0.9;
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--border);
  }
</style>
