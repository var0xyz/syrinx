<script lang="ts">
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import { reedsService } from '$lib/repositories/reeds';
  import { reedRepliesRepository } from '$lib/repositories/reedReplies';
  import { userRepository } from '$lib/repositories/user';
  import { removedReedsRepository } from '$lib/repositories/removedReeds';
  import { serverConnection } from '$lib/services/serverConnection';
  import { formatRelativeTime } from '$lib/utils/time';
  import type { ReedType } from '$lib/types/reed';

  const tabs = [
    { href: '/replies', label: 'Replies' },
    { href: '/ripples', label: 'Ripples' },
    { href: '/feed/mentions', label: 'Mentions' },
  ];

  type Row = {
    reedID: string;
    authorID: string;
    parentReedID: string;
    reed: ReedType | null;
    username: string;
    loading: boolean;
  };

  let rows: Row[] = [];
  let loading = true;

  async function toRow(reedID: string, parentReedID: string): Promise<Row | null> {
    if (await removedReedsRepository.has(reedID)) return null;
    let reed = await reedsService.getReed(reedID);
    if (!reed) {
      reed = await serverConnection.requestReedContent(reedID).catch(() => null);
    }
    if (!reed) return null;
    const username =
      (await userRepository.getByUserId(reed.userID).catch(() => null))?.username ?? reed.userID;
    return { reedID, authorID: reed.userID, parentReedID, reed, username, loading: false };
  }

  async function loadReplies() {
    loading = true;
    try {
      const myUserID = localStorage.getItem('userId') ?? '';
      const myReeds = await reedsService.getReedsByAuthor(myUserID);
      const replyRows = await reedRepliesRepository.listByParents(myReeds.map((r) => r.id));

      const resolved = await Promise.all(
        replyRows.map((r) => toRow(r.reedID, r.parentReedID)),
      );
      rows = resolved
        .filter((r): r is Row => r !== null)
        .sort((a, b) => (b.reed!.serverSignature?.timestamp ?? '').localeCompare(
          a.reed!.serverSignature?.timestamp ?? '',
        ));
    } finally {
      loading = false;
    }
  }

  function navigateToReply(row: Row) {
    goto(`/reed/${row.reedID}`);
  }

  loadReplies();
</script>

<Auth>
  <SideNav currentPage="interactions" />
  <div class="replies-container">
    <SectionTabs {tabs} active="replies" />

    <div class="replies-content">
      {#if loading}
        <div class="loading-state">
          <p>Loading…</p>
        </div>
      {:else if rows.length === 0}
        <div class="empty-state">
          <div class="empty-icon">💬</div>
          <h3>No replies yet</h3>
          <p>Replies to your reeds will appear here.</p>
        </div>
      {:else}
        <ul class="reply-list">
          {#each rows as row (row.reedID)}
            <li>
              <div
                class="reply-row"
                role="button"
                tabindex="0"
                on:click={() => navigateToReply(row)}
                on:keydown={(e) => e.key === 'Enter' && navigateToReply(row)}
              >
                <ReedAuthorHeader
                  userID={row.authorID}
                  username={row.username}
                  avatarSize="36px"
                  subtext={row.reed?.serverSignature?.timestamp
                    ? formatRelativeTime(row.reed.serverSignature.timestamp)
                    : ''}
                  stopPropagation
                  linked={false}
                />
                <div class="reply-body">
                  {#if row.reed?.content?.trim()}
                    <div class="reply-preview">
                      <MarkdownParser text={row.reed.content} preview={true} />
                    </div>
                  {:else}
                    <p class="reply-preview muted">Empty reply</p>
                  {/if}
                </div>
                <div class="reply-quote">
                  <Quote reedRef={row.parentReedID} type="reply" linked={false} />
                </div>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <BottomToolbar currentPage="interactions" />
  </div>
</Auth>

<style>
  .replies-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
    gap: 0.5rem;
  }

  .replies-content {
    margin: 0 0.5rem;
  }

  @media (min-width: 768px) {
    .replies-container {
      padding-left: calc(var(--sidenav-width) + 1rem);
    }
  }

  @media (min-width: 1400px) {
    .replies-container {
      padding-right: calc(var(--activity-sidebar-width) + 1rem);
    }
  }

  .loading-state,
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

  .reply-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .reply-row {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    width: 100%;
    text-align: left;
    font: inherit;
    color: var(--fg);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.75rem;
    cursor: pointer;
  }

  .reply-row:hover {
    background: var(--input-bg);
  }

  .reply-body {
    min-width: 0;
  }

  .reply-preview {
    font-size: 0.9rem;
    overflow: hidden;
    color: var(--fg);
  }

  .reply-preview.muted {
    color: var(--muted);
  }

  .reply-quote {
    min-width: 0;
  }
</style>
