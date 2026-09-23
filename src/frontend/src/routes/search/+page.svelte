<script lang="ts">
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import LocalPagination from '$lib/components/LocalPagination.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { localSearchRepository } from '$lib/repositories/localSearch';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import type { ReedType } from '$lib/types/reed';
  import type * as api from '$lib/types/api';

  const modeOptions = [
    { value: 'reeds', label: 'Reeds' },
    { value: 'users', label: 'Users' },
  ];

  const PAGE_SIZE = 30;

  type ReedRow = { reed: ReedType; username: string };

  let mode: 'reeds' | 'users' = 'reeds';
  let query = '';
  let submittedQuery = '';
  let hasSearched = false;
  let reedsPagination: LocalPagination<ReedRow> | undefined;
  let usersPagination: LocalPagination<api.User> | undefined;

  async function resolveReedRows(reeds: ReedType[]): Promise<ReedRow[]> {
    const authorIds = [...new Set(reeds.map((reed) => reed.userID))];
    const authorResults = await Promise.allSettled(
      authorIds.map((id) => userRepository.getByUserId(id))
    );
    const authorMap = new Map<string, string>();
    authorIds.forEach((id, i) => {
      const result = authorResults[i];
      if (result.status === 'fulfilled' && result.value) {
        authorMap.set(id, result.value.username);
      }
    });
    return reeds.map((reed) => ({ reed, username: authorMap.get(reed.userID) ?? reed.userID }));
  }

  async function fetchReedsPage(after?: string) {
    const page = await localSearchRepository.searchReeds(submittedQuery, PAGE_SIZE, after);
    const items = await resolveReedRows(page.items);
    return { items, hasMore: page.hasMore, nextCursor: page.nextCursor };
  }

  async function fetchUsersPage(after?: string) {
    return localSearchRepository.searchUsers(submittedQuery, PAGE_SIZE, after);
  }

  function submit() {
    const trimmed = query.trim();
    if (!trimmed) return;
    submittedQuery = trimmed;
    hasSearched = true;
    if (mode === 'reeds') {
      void reedsPagination?.loadFirstPage();
    } else {
      void usersPagination?.loadFirstPage();
    }
  }

  function clear() {
    query = '';
    submittedQuery = '';
    hasSearched = false;
  }

  function navigateToReed(reed: ReedType) {
    goto(`/reed/${reed.id}`);
  }
</script>

<Auth>
  <SideNav currentPage="search" />
  <div class="search-container">
    <div class="search-content">
      <form class="search-form" on:submit|preventDefault={submit}>
        <input
          type="text"
          bind:value={query}
          placeholder={mode === 'reeds' ? 'Search reed content…' : 'Search username or bio…'}
          aria-label={mode === 'reeds' ? 'Search reed content' : 'Search users'}
        />
        <div class="search-actions">
          <button type="button" class="btn btn-secondary" on:click={clear}>
            Clear
          </button>
          <button type="submit" class="btn btn-primary" disabled={!query.trim()}>Search</button>
        </div>
      </form>

      {#if !hasSearched}
        <div class="mode-picker">
          <ModeSwitch options={modeOptions} bind:value={mode} />
        </div>
      {:else if mode === 'reeds'}
        <LocalPagination bind:this={reedsPagination} fetchPage={fetchReedsPage}>
          {#snippet item(row: ReedRow)}
            <div
              class="reed-item"
              role="button"
              tabindex="0"
              on:click={() => navigateToReed(row.reed)}
              on:keydown={(e) => e.key === 'Enter' && navigateToReed(row.reed)}
            >
              <ReedAuthorHeader
                userID={row.reed.userID}
                username={row.username}
                nameTag="h3"
                subtext={row.reed.serverSignature?.timestamp
                  ? formatRelativeTime(row.reed.serverSignature.timestamp)
                  : ''}
                stopPropagation
                linked={false}
              />
              {#if (row.reed.content || '').trim()}
                <div class="reed-preview">
                  <MarkdownParser text={row.reed.content} preview={true} />
                </div>
              {/if}
            </div>
          {/snippet}
          {#snippet empty()}
            <div class="empty-state">
              <div class="empty-icon">🔍</div>
              <h3>No matching reeds</h3>
              <p>No stored reeds contain "{submittedQuery}".</p>
            </div>
          {/snippet}
        </LocalPagination>
      {:else}
        <LocalPagination bind:this={usersPagination} fetchPage={fetchUsersPage}>
          {#snippet item(user: api.User)}
            <div class="user-item">
              <ReedAuthorHeader userID={user.id} username={user.username} subtext={user.bio} nameTag="h3" />
            </div>
          {/snippet}
          {#snippet empty()}
            <div class="empty-state">
              <div class="empty-icon">🔍</div>
              <h3>No matching users</h3>
              <p>No cached users match "{submittedQuery}".</p>
            </div>
          {/snippet}
        </LocalPagination>
      {/if}
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
    width: 100%;
    max-width: 680px;
    margin: 0 auto;
    padding: 0 0.5rem;
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

  .search-form {
    display: flex;
    gap: 0.5rem;
    margin: 0.75rem 0;
    flex-wrap: wrap;
  }

  .search-form input {
    flex: 1;
    min-width: 0;
    padding: 0.6rem 0.75rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
  }

  .search-actions {
    display: flex;
    gap: 0.5rem;
    flex: 1 0 100%;
  }

  @media (min-width: 768px) {
    .search-actions {
      flex: 0 0 auto;
    }
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
    flex: 1;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-secondary {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .mode-picker {
    display: flex;
    justify-content: center;
    padding: 2rem 0;
  }

  .reed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
    margin-bottom: 1rem;
    cursor: pointer;
    transition: all 0.2s ease;
  }

  .reed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .reed-preview {
    margin-top: 0.75rem;
    word-break: break-word;
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
