<script>
  import { createEventDispatcher } from 'svelte';

  export let message = 'New reed available';

  const dispatch = createEventDispatcher();
</script>

<div class="new-reed-banner">
  <div class="new-reed-msg">{message}</div>
  <button on:click={() => dispatch('show')}>Show</button>
  <button class="dismiss" on:click={() => dispatch('dismiss')}>✕</button>
</div>

<style>
  .new-reed-banner {
    position: fixed;
    top: 1rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    background: var(--surface);
    border: 1px solid var(--primary);
    border-radius: 8px;
    font-size: 0.9rem;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    width: calc(100vw - 2.5rem);
  }

  .new-reed-msg {
    flex-grow: 1;
    color: var(--fg);
  }

  button {
    flex-shrink: 0;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    border-radius: 4px;
    padding: 0.25rem 0.75rem;
    cursor: pointer;
    font-size: 0.85rem;
    white-space: nowrap;
    width: 5rem;
  }

  button.dismiss {
    background: none;
    color: var(--muted);
    padding: 0.25rem;
    width: 2rem;
  }
</style>
