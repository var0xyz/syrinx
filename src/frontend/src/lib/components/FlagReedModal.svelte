<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { flagReed } from '$lib/services/flags';

  export let open = false;
  export let reedID: string;

  const MAX_NOTE = 280;
  const dispatch = createEventDispatcher<{ close: void }>();

  let note = '';
  let sending = false;
  let result = '';
  let error = '';

  function close() {
    note = '';
    result = '';
    error = '';
    dispatch('close');
  }

  async function send() {
    sending = true;
    error = '';
    try {
      result = (await flagReed(reedID, note.trim()))
        ? 'Flagged. The admins will take a look.'
        : 'You already flagged this reed.';
    } catch (err) {
      error = err instanceof Error ? err.message : 'Could not flag this reed.';
    } finally {
      sending = false;
    }
  }
</script>

{#if open}
  <!-- Rendered inside a reed card, which navigates on click: keep clicks here. -->
  <div class="overlay" on:click|stopPropagation={close} role="presentation"></div>
  <div
    class="modal"
    role="dialog"
    aria-modal="true"
    aria-labelledby="flag-title"
    tabindex="-1"
    on:click|stopPropagation
    on:keydown|stopPropagation
  >
    <h3 id="flag-title">Flag this reed</h3>
    {#if result}
      <p class="result">{result}</p>
      <div class="actions">
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else}
      <label class="field">
        <span>Note for the admins (optional)</span>
        <textarea bind:value={note} rows="3" maxlength={MAX_NOTE}></textarea>
      </label>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={close}>Cancel</button>
        <button class="btn btn-primary" type="button" disabled={sending} on:click={send}>
          {sending ? 'Flagging…' : 'Flag'}
        </button>
      </div>
    {/if}
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
    text-align: left;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .field textarea {
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.9rem;
    resize: vertical;
  }

  .result {
    margin: 0;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .error {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
  }

  .btn {
    width: auto;
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-family: inherit;
    font-weight: 600;
    font-size: 0.9rem;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
