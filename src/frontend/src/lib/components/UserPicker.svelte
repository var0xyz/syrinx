<script lang="ts">
  import { onDestroy } from 'svelte';
  import { apiService } from '$lib/services/api';
  import type * as api from '$lib/types/api';

  /** Bound two-way: what's typed in the field. */
  export let value = '';
  /** Bound two-way: the picked member's ID, cleared once the text changes. */
  export let userID = '';
  /** Only offer members of this server. */
  export let local = false;
  /** Leave the root operator out of the results. */
  export let excludeRoot = false;
  export let placeholder = 'Username';

  const MAX_USERNAME_LENGTH = 32;
  const DEBOUNCE_MS = 200;

  let open = false;
  let results: api.UserSearchResult[] = [];
  let highlighted = 0;
  let loading = false;
  let container: HTMLDivElement;
  let debounceTimeout: ReturnType<typeof setTimeout> | null = null;
  let currentSearch: AbortController | null = null;
  let picked = '';
  const listID = `user-picker-${Math.random().toString(36).slice(2)}`;

  function closePicker(): void {
    open = false;
    results = [];
    highlighted = 0;
    if (debounceTimeout) clearTimeout(debounceTimeout);
    debounceTimeout = null;
    currentSearch?.abort();
    currentSearch = null;
  }

  function handleInput(): void {
    if (value !== picked) userID = '';
    const query = value.trim().replace(/^@/, '');
    if (query.length === 0 || query.length > MAX_USERNAME_LENGTH) {
      closePicker();
      return;
    }
    open = true;
    loading = true;
    if (debounceTimeout) clearTimeout(debounceTimeout);
    currentSearch?.abort();
    debounceTimeout = setTimeout(() => runSearch(query), DEBOUNCE_MS);
  }

  async function runSearch(query: string): Promise<void> {
    const controller = new AbortController();
    currentSearch = controller;
    try {
      const res = await apiService.searchUsers(query, 20, undefined, { local, excludeRoot });
      if (controller.signal.aborted) return;
      results = res.users || [];
      highlighted = 0;
    } catch {
      if (controller.signal.aborted) return;
      results = [];
    } finally {
      if (currentSearch === controller) {
        currentSearch = null;
        loading = false;
      }
    }
  }

  function confirm(user: api.UserSearchResult): void {
    value = picked = user.username;
    userID = user.id;
    closePicker();
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (!open) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closePicker();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (results.length > 0) highlighted = (highlighted + 1) % results.length;
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (results.length > 0) highlighted = (highlighted - 1 + results.length) % results.length;
    } else if ((e.key === 'Enter' || e.key === 'Tab') && results.length > 0) {
      e.preventDefault();
      confirm(results[highlighted]);
    }
  }

  function handleWindowClick(e: MouseEvent): void {
    if (open && container && !container.contains(e.target as Node)) closePicker();
  }

  onDestroy(closePicker);
</script>

<svelte:window on:click={handleWindowClick} />

<div class="user-picker" bind:this={container}>
  <input
    type="text"
    bind:value
    on:input={handleInput}
    on:keydown={handleKeydown}
    {placeholder}
    autocomplete="off"
    spellcheck="false"
    role="combobox"
    aria-expanded={open}
    aria-controls={listID}
    aria-autocomplete="list"
  />
  {#if open}
    <div class="user-popover" id={listID} role="listbox">
      {#if loading}
        <div class="user-status">Searching…</div>
      {:else if results.length === 0}
        <div class="user-status">No matches</div>
      {:else}
        {#each results as user, i (user.id)}
          <button
            type="button"
            class="user-result"
            class:highlighted={i === highlighted}
            role="option"
            aria-selected={i === highlighted}
            on:mouseenter={() => (highlighted = i)}
            on:click={() => confirm(user)}
          >
            {user.username}<span class="user-result-server">· {user.serverName}</span>
          </button>
        {/each}
      {/if}
    </div>
  {/if}
</div>

<style>
  .user-picker {
    position: relative;
  }

  .user-picker input {
    width: 100%;
  }

  .user-popover {
    position: absolute;
    top: calc(100% + 0.25rem);
    left: 0;
    z-index: 1100;
    min-width: 160px;
    max-width: 280px;
    max-height: 220px;
    overflow-y: auto;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
    padding: 0.25rem;
  }

  .user-status {
    padding: 0.5rem 0.75rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .user-result {
    display: block;
    width: 100%;
    text-align: left;
    padding: 0.5rem 0.75rem;
    border: none;
    background: none;
    color: var(--fg);
    font-size: 0.9rem;
    border-radius: 6px;
    cursor: pointer;
  }

  .user-result.highlighted,
  .user-result:hover {
    background: var(--input-bg);
  }

  .user-result-server {
    color: var(--muted);
    margin-left: 0.25rem;
  }
</style>
