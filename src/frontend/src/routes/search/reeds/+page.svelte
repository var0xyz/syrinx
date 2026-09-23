<script lang="ts">
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import LocalPagination from '$lib/components/LocalPagination.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { localSearchRepository } from '$lib/repositories/localSearch';
  import { userRepository } from '$lib/repositories/user';
  import { formatRelativeTime } from '$lib/utils/time';
  import type { ReedType } from '$lib/types/reed';

  const tabs = [
    { href: '/search/reeds', label: 'Reeds' },
    { href: '/search/users', label: 'Users' },
  ];

  const PAGE_SIZE = 30;

  type Row = { reed: ReedType; username: string };

  let query = '';
  let submittedQuery = '';
  let pagination: LocalPagination<Row> | undefined;

  async function resolveRows(reeds: ReedType[]): Promise<Row[]> {
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

  async function fetchPage(after?: string) {
    const page = await localSearchRepository.searchReeds(submittedQuery, PAGE_SIZE, after);
    const items = await resolveRows(page.items);
    return { items, hasMore: page.hasMore, nextCursor: page.nextCursor };
  }

  function submit() {
    submittedQuery = query.trim();
    void pagination?.loadFirstPage();
  }

  function navigateToReed(reed: ReedType) {
    goto(`/reed/${reed.id}`);
  }
</script>

<Auth>
  <SideNav currentPage="search" />
  <div class="search-container">
    <SectionTabs {tabs} active="reeds" />

    <div class="search-content">
      <p class="local-notice">
        Searches only the reeds already stored on this device — not a system-wide search.
      </p>

      <form class="search-form" on:submit|preventDefault={submit}>
        <input
          type="search"
          bind:value={query}
          placeholder="Search reed content…"
          aria-label="Search reed content"
        />
        <button type="submit" class="btn btn-primary" disabled={!query.trim()}>Search</button>
      </form>

      <LocalPagination bind:this={pagination} {fetchPage}>
        {#snippet item(row: Row)}
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
            {#if submittedQuery}
              <h3>No matching reeds</h3>
              <p>No stored reeds contain “{submittedQuery}”.</p>
            {:else}
              <h3>Search your stored reeds</h3>
              <p>Type something above to search reed content on this device.</p>
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
