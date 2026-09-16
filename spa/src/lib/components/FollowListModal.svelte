<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import RemotePagination from '$lib/components/RemotePagination.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  export let userId = '';
  /** 'following' | 'followers' */
  export let mode: 'following' | 'followers' = 'following';

  const dispatch = createEventDispatcher();

  type Row = { userID: string; username: string; followedAt: string };

  let pagination: RemotePagination<Row> | undefined;
  let loadedForKey = '';

  $: title = mode === 'following' ? 'Following' : 'Followers';

  // Fires once on mount and again if the parent ever reuses a
  // still-mounted instance for a different user/mode.
  $: key = `${userId}:${mode}`;
  $: if (key !== loadedForKey) {
    loadedForKey = key;
    void pagination?.loadFirstPage();
  }

  async function fetchFollowPage(cursor?: string) {
    if (!userId) return { items: [], hasMore: false };
    const list = mode === 'following'
      ? await apiService.listFollowing(userId, { before: cursor })
      : await apiService.listFollowers(userId, { before: cursor });

    const items = await Promise.all(
      list.users.map(async (u) => {
        const profile = await userRepository.getByUserId(u.userID).catch(() => null);
        return {
          userID: u.userID,
          username: profile?.username ?? u.userID,
          followedAt: u.followedAt,
        };
      })
    );

    const nextCursor = list.users.length > 0 ? list.users[list.users.length - 1].followedAt : cursor;
    return { items, hasMore: list.hasMore, nextCursor };
  }

  function close() {
    dispatch('close');
  }
</script>

<div
  class="modal-backdrop"
  role="dialog"
  aria-modal="true"
  aria-labelledby="follow-list-modal-title"
  tabindex="-1"
  on:click={(e) => e.target === e.currentTarget && close()}
  on:keydown={(e) => e.key === 'Escape' && close()}
>
  <div class="modal">
    <div class="modal-header">
      <h2 id="follow-list-modal-title">{title}</h2>
      <button class="close-btn" aria-label="Close" on:click={close}>✕</button>
    </div>

    <div class="list-body">
      <RemotePagination bind:this={pagination} fetchPage={fetchFollowPage}>
        {#snippet item(row)}
          <div
            class="user-row"
            role="button"
            tabindex="0"
            on:click={() => goto(`/profile/${row.userID}`)}
            on:keydown={(e) => e.key === 'Enter' && goto(`/profile/${row.userID}`)}
          >
            <ReedAuthorHeader
              userID={row.userID}
              username={row.username}
              subtext={`Since ${formatRelativeTime(row.followedAt)}`}
              stopPropagation
            />
          </div>
        {/snippet}
        {#snippet empty()}
          <p class="state-text">
            {mode === 'following' ? 'Not following anyone yet.' : 'No followers yet.'}
          </p>
        {/snippet}
      </RemotePagination>
    </div>
  </div>
</div>

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
    padding: 1rem;
  }

  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.25rem 0.25rem 0.75rem 0.75rem;
    max-width: 420px;
    width: 100%;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
  }

  .modal-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .modal-header h2 {
    flex: 1;
    margin: 0;
    font-size: 1.2rem;
    color: var(--fg);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .close-btn {
    flex-shrink: 0;
    width: 2rem;
    height: 2rem;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: var(--muted);
    font-size: 1rem;
    cursor: pointer;
    border-radius: 6px;
    transition: color 0.2s ease, background 0.2s ease;
  }

  .close-btn:hover {
    color: var(--fg);
    background: var(--border);
  }

  .list-body {
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }

  .user-row {
    display: block;
    text-decoration: none;
    color: inherit;
    padding: 0.4rem;
    border-radius: 8px;
    cursor: pointer;
  }

  .user-row:hover {
    background: var(--input-bg);
  }

  .state-text {
    color: var(--muted);
    text-align: center;
    padding: 1rem 0;
    margin: 0;
  }
</style>
