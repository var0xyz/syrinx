<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import LocalPagination from '$lib/components/LocalPagination.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import { localSearchRepository } from '$lib/repositories/localSearch';
  import type * as api from '$lib/types/api';

  const tabs = [
    { href: '/search/reeds', label: 'Reeds' },
    { href: '/search/users', label: 'Users' },
  ];

  const PAGE_SIZE = 30;

  let query = '';
  let submittedQuery = '';
  let pagination: LocalPagination<api.User> | undefined;

  async function fetchPage(after?: string) {
    return localSearchRepository.searchUsers(submittedQuery, PAGE_SIZE, after);
  }

  function submit() {
    submittedQuery = query.trim();
    void pagination?.loadFirstPage();
  }
</script>

<Auth>
  <SideNav currentPage="search" />
  <div class="search-container">
    <SectionTabs {tabs} active="users" />

    <div class="search-content">
      <p class="local-notice">
        Searches only users already cached on this device (people you've followed, seen reeds
        from, or interacted with) — not a system-wide directory.
      </p>

      <form class="search-form" on:submit|preventDefault={submit}>
        <input
          type="search"
          bind:value={query}
          placeholder="Search username or bio…"
          aria-label="Search users"
        />
        <button type="submit" class="btn btn-primary" disabled={!query.trim()}>Search</button>
      </form>

      <LocalPagination bind:this={pagination} {fetchPage}>
        {#snippet item(user: api.User)}
          <div class="user-item">
            <ReedAuthorHeader userID={user.id} username={user.username} subtext={user.bio} nameTag="h3" />
          </div>
        {/snippet}
        {#snippet empty()}
          <div class="empty-state">
            <div class="empty-icon">🔍</div>
            {#if submittedQuery}
              <h3>No matching users</h3>
              <p>No cached users match “{submittedQuery}”.</p>
            {:else}
              <h3>Search cached users</h3>
              <p>Type something above to search users seen on this device.</p>
            {/if}
          </div>
        {/snippet}
      </LocalPagination>
    </div>
  </div>
</Auth>

<BottomToolbar currentPage="search" />

<style>
  .search-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
    gap: 0.5rem;
  }

  .search-content {
    margin: 0 0.5rem;
  }

  @media (min-width: 768px) {
    .search-container {
      padding-left: calc(var(--sidenav-width) + 1rem);
    }
  }

  @media (min-width: 1400px) {
    .search-container {
      padding-right: calc(var(--activity-sidebar-width) + 1rem);
    }
  }

  .local-notice {
    margin: 0.75rem 0 0 0;
    padding: 0.5rem 0.75rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .search-form {
    display: flex;
    gap: 0.5rem;
    margin: 0.75rem 0;
  }

  .search-form input {
    flex: 1;
    padding: 0.6rem 0.75rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.6rem 1.25rem;
    border-radius: 8px;
    font-weight: 600;
    border: none;
    cursor: pointer;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .user-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
    margin-bottom: 1rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }
</style>
