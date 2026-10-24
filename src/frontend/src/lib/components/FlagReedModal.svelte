<script lang="ts">
  import { countCharacters } from '$lib/utils/text';
  import { createEventDispatcher } from 'svelte';
  import { fetchOwnFlag, flagReed, withdrawFlag } from '$lib/services/flags';
  import type { StoredFlag } from '$lib/repositories/flags';
  import { formatAbsoluteDateTime, fromUnix } from '$lib/utils/time';

  export let open = false;
  export let reedID: string;
  /** The viewer's own flag on reedID; once withdrawn it can't be redone. */
  export let flag: StoredFlag | null = null;

  const MAX_NOTE = 140;
  const dispatch = createEventDispatcher<{ close: void }>();

  let note = '';
  let sending = false;
  let result = '';
  let error = '';
  let confirming = false;
  let acknowledged = false;
  // A flag known only from a 409 is fetched once, for its dates and note.
  let fetched = '';

  $: if (open && flag && !flag.serverSignature && fetched !== reedID) {
    fetched = reedID;
    fetchOwnFlag(reedID).catch((err) => console.error('Failed to fetch own flag:', err));
  }

  function close() {
    note = '';
    result = '';
    error = '';
    confirming = false;
    acknowledged = false;
    dispatch('close');
  }

  function date(signedAt: number | undefined): string {
    return signedAt ? formatAbsoluteDateTime(fromUnix(signedAt)) : 'an unknown date';
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

  async function withdraw() {
    sending = true;
    error = '';
    try {
      await withdrawFlag(reedID);
      close();
    } catch (err) {
      const message = err instanceof Error ? err.message : '';
      if (message === 'flag-acknowledged') acknowledged = true;
      else error = message || 'Could not withdraw your flag.';
      confirming = false;
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
    {#if result}
      <h3 id="flag-title">Flag this reed</h3>
      <p class="result">{result}</p>
      <div class="actions">
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else if flag?.withdrawal}
      <h3 id="flag-title">Flag this reed</h3>
      <p class="result">You flagged this reed on {date(flag.serverSignature?.signedAt)}.</p>
      <p class="result">You withdrew it on {date(flag.withdrawal.serverSignature?.signedAt)}.</p>
      <p class="hint">A withdrawn flag is final, so you can’t flag this reed again.</p>
      <div class="actions">
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else if flag && confirming}
      <h3 id="flag-title">Withdraw your flag?</h3>
      <p class="result">This can’t be undone, and you won’t be able to flag this reed again.</p>
      <div class="actions">
        <button class="btn btn-secondary" type="button" disabled={sending} on:click={close}>Cancel</button>
        <button class="btn btn-danger" type="button" disabled={sending} on:click={withdraw}>
          {sending ? 'Withdrawing…' : 'Withdraw'}
        </button>
      </div>
    {:else if flag}
      <h3 id="flag-title">You flagged this reed</h3>
      {#if flag.serverSignature}<p class="hint">{date(flag.serverSignature.signedAt)}</p>{/if}
      <p class="note" class:empty={!flag.note}>{flag.note ? `“${flag.note}”` : 'No note'}</p>
      {#if acknowledged}
        <p class="error" role="alert">Admins have already acted on your flag, so it can’t be withdrawn.</p>
      {:else if error}
        <p class="error" role="alert">{error}</p>
      {/if}
      <div class="actions">
        {#if !acknowledged}
          <button class="btn btn-secondary" type="button" on:click={() => (confirming = true)}>Withdraw</button>
        {/if}
        <button class="btn btn-primary" type="button" on:click={close}>Done</button>
      </div>
    {:else}
      <h3 id="flag-title">Flag this reed</h3>
      <label class="field">
        <span>Note for the admins (optional)</span>
        <textarea bind:value={note} rows="3"></textarea>
        <span class="character-counter">{countCharacters(note)}/{MAX_NOTE} characters</span>
      </label>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="actions">
        <button class="btn btn-secondary" type="button" on:click={close}>Cancel</button>
        <button class="btn btn-primary" type="button" disabled={sending || countCharacters(note) > MAX_NOTE} on:click={send}>
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

  .character-counter {
    text-align: right;
    font-size: 0.8rem;
    color: var(--muted);
  }

  .result {
    margin: 0;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .note {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
    overflow-wrap: anywhere;
  }

  .note.empty {
    font-style: italic;
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

  .btn-danger {
    background: var(--error);
    color: #fff;
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
