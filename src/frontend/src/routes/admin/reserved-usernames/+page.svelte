<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import { apiService } from '$lib/services/api';

  let names: string[] = [];
  let newName = '';
  let busy = false;
  let error = '';

  $: isRoot = $page.data?.user?.role === 'root';

  onMount(() => {
    if (isRoot) void load();
  });

  async function load(): Promise<void> {
    try {
      names = await apiService.listReservedUsernames();
    } catch (err) {
      error = err instanceof Error ? err.message : 'Could not load reserved usernames.';
    }
  }

  async function change(name: string, reserved: boolean): Promise<void> {
    busy = true;
    error = '';
    try {
      await apiService.setReservedUsername(name, reserved);
      if (reserved) newName = '';
      await load();
    } catch (err) {
      error = err instanceof Error ? err.message : 'The change was refused.';
    } finally {
      busy = false;
    }
  }
</script>

<Auth>
  <SideNav currentPage="admin" />
  <div class="admin-container">
    <div class="admin-content">
      <h1 class="title">Reserved usernames</h1>
      {#if !isRoot}
        <p class="error" role="alert">Only root can reserve usernames.</p>
      {:else}
        <form class="add" on:submit|preventDefault={() => change(newName.trim().toLowerCase(), true)}>
          <input type="text" bind:value={newName} placeholder="Username" autocomplete="off" spellcheck="false" maxlength="32" />
          <button class="btn primary" type="submit" disabled={busy || !newName.trim()}>Reserve</button>
        </form>
        {#if error}<p class="error" role="alert">{error}</p>{/if}

        {#if names.length === 0}
          <p class="empty">No usernames reserved.</p>
        {:else}
          <ul class="names">
            {#each names as name (name)}
              <li>
                <span class="name">{name}</span>
                <button class="btn secondary" type="button" disabled={busy} on:click={() => change(name, false)}>
                  Release
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>
</Auth>

<style>
  .admin-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .admin-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .admin-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .admin-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .title {
    margin: 0;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .add {
    display: flex;
    gap: 0.5rem;
  }

  .add input {
    flex: 1;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
  }

  .names {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .names li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
  }

  .names li + li {
    border-top: 1px solid var(--border);
  }

  .name {
    color: var(--fg);
    font-weight: 600;
  }

  .empty {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .error {
    margin: 0;
    color: var(--error);
    font-size: 0.9rem;
  }

  .btn {
    width: auto;
    border: none;
    border-radius: 8px;
    padding: 0.5rem 1rem;
    font-family: inherit;
    font-weight: 600;
    cursor: pointer;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn.secondary {
    background: transparent;
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
