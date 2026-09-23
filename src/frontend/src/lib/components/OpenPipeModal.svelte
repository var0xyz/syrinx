<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { dbService } from '$lib/services/db';
  import { normalizePipeTag } from '$lib/utils/pipeTag';

  const dispatch = createEventDispatcher();

  let query = '';
  let allTags: string[] = [];
  let suggestionsDismissed = false;
  let selectionMade = false;
  let errorMessage = '';

  onMount(async () => {
    const tags = await dbService.getAll<{ tagName: string; displayName?: string }>('tags');
    allTags = tags.map((t) => t.displayName ?? t.tagName).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));
  });

  $: normalized = normalizePipeTag(query);
  $: suggestions = normalized
    ? allTags.filter((t) => t.toLowerCase().includes(normalized)).slice(0, 8)
    : allTags.slice(0, 8);
  $: showSuggestions = !suggestionsDismissed && !selectionMade && suggestions.length > 0;

  function pick(tag: string) {
    query = tag;
    selectionMade = true;
  }

  function toggleSuggestions() {
    suggestionsDismissed = !suggestionsDismissed;
  }

  function open() {
    if (!normalized) {
      errorMessage = query.trim()
        ? 'Tags can\'t contain spaces.'
        : 'Enter a tag.';
      return;
    }
    goto(`/feed/pipes/${encodeURIComponent(normalized)}`);
    dispatch('cancel');
  }

  function cancel() {
    dispatch('cancel');
  }
</script>

<div class="overlay" on:click={cancel} role="presentation"></div>
<div class="modal" role="dialog" aria-modal="true" aria-labelledby="open-pipe-title">
  <h3 id="open-pipe-title">Open hashtag</h3>

  <div class="field">
    <label for="pipe-tag">Tag</label>
    <div class="autocomplete">
      <input
        id="pipe-tag"
        type="text"
        bind:value={query}
        placeholder="hashtag"
        autocomplete="off"
        on:input={() => {
          selectionMade = false;
          errorMessage = '';
        }}
        on:keydown={(e) => e.key === 'Enter' && open()}
      />
      {#if suggestions.length > 0}
        <button
          type="button"
          class="suggestions-toggle"
          on:mousedown|preventDefault={toggleSuggestions}
          aria-label={showSuggestions ? 'Hide suggestions' : 'Show suggestions'}
          aria-expanded={showSuggestions}
        >
          <span class="chevron" class:flipped={showSuggestions}></span>
        </button>
      {/if}
      {#if showSuggestions}
        <ul class="suggestions">
          {#each suggestions as tag (tag)}
            <li>
              <button type="button" on:click={() => pick(tag)}>#{tag}</button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    {#if errorMessage}
      <p class="field-error">{errorMessage}</p>
    {/if}
  </div>

  <div class="actions">
    <button class="btn btn-secondary" on:click={cancel}>Cancel</button>
    <button class="btn btn-primary" on:click={open}>Open</button>
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
    width: min(420px, calc(100vw - 2rem));
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    overflow-y: visible;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .field label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--fg);
  }

  .field-error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--error);
  }

  .autocomplete {
    position: relative;
  }

  .field input[type='text'] {
    width: 100%;
    box-sizing: border-box;
    padding: 0.6rem 2.25rem 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
    font-family: inherit;
  }

  .field input[type='text']:focus {
    outline: none;
    border-color: var(--primary);
  }

  .suggestions-toggle {
    position: absolute;
    top: 0;
    right: 0;
    height: 100%;
    width: 2.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    cursor: pointer;
    color: var(--muted);
  }

  .suggestions-toggle:hover {
    color: var(--fg);
  }

  .chevron {
    display: inline-block;
    width: 0;
    height: 0;
    border-left: 4px solid transparent;
    border-right: 4px solid transparent;
    border-top: 5px solid currentColor;
    transition: transform 0.15s ease;
  }

  .chevron.flipped {
    transform: rotate(180deg);
  }

  .suggestions {
    position: absolute;
    top: calc(100% + 0.25rem);
    left: 0;
    right: 0;
    margin: 0;
    padding: 0.25rem;
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    max-height: 200px;
    overflow-y: auto;
    z-index: 1;
  }

  .suggestions li button {
    display: block;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.5rem 0.6rem;
    border-radius: 6px;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .suggestions li button:hover {
    background: var(--input-bg);
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

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:not(:disabled):hover {
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
