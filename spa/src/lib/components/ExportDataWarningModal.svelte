<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let open: boolean = false;

  const dispatch = createEventDispatcher();

  function confirm() {
    dispatch('confirm');
  }

  function cancel() {
    dispatch('cancel');
  }
</script>

{#if open}
  <div class="overlay" on:click={cancel} role="presentation"></div>
  <div class="modal" role="dialog" aria-modal="true">
    <h3>⚠️ Security Warning</h3>
    <p>This backup contains your <strong>private encryption keys</strong>.</p>
    <p>Anyone who obtains this file will be able to <strong>impersonate you</strong> and <strong>decrypt your private data</strong>. Store it in a safe place, such as an encrypted drive or password manager.</p>
    <p>Do not share it with anyone.</p>
    <div class="actions">
      <button class="btn btn-secondary" on:click={cancel}>Cancel</button>
      <button class="btn btn-primary" on:click={confirm}>I understand, export</button>
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

  p {
    margin: 0;
    font-size: 0.9rem;
    line-height: 1.5;
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
