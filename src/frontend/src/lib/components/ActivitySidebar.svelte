<script>
  import { goto } from '$app/navigation';
  import { getActivity } from '$lib/repositories/activity';
  import { followReedQueue, pipeReedQueue, reedReplyQueue, profileReedQueue } from '$lib/repositories/reeds';
  import { dbService } from '$lib/services/db';
  import { formatRelativeTime } from '$lib/utils/time';
  import { stripMarkdown } from '$lib/utils/reedContent';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';

  /** @type {import('$lib/types/reed').ReedType[]} */
  let reeds = [];
  /** @type {Record<string, import('$lib/types/api').User>} */
  let authors = {};
  let paused = false;
  /** Set while hovered so an in-flight refresh doesn't land mid-hover. */
  let pendingRefresh = false;
  let lastHandledReedId = '';

  async function refresh() {
    if (paused) {
      pendingRefresh = true;
      return;
    }
    reeds = await getActivity();
    const resolved = {};
    for (const reed of reeds) {
      if (!resolved[reed.userID]) {
        const user = await dbService.get('users', reed.userID);
        if (user) resolved[reed.userID] = user;
      }
    }
    authors = resolved;
  }

  function onMouseEnter() {
    paused = true;
  }

  function onMouseLeave() {
    paused = false;
    if (pendingRefresh) {
      pendingRefresh = false;
      void refresh();
    }
  }

  $: arrived = $followReedQueue?.reed ?? $pipeReedQueue?.reed ?? $reedReplyQueue?.reed ?? $profileReedQueue?.reed;
  $: if (arrived && arrived.id !== lastHandledReedId) {
    lastHandledReedId = arrived.id;
    void refresh();
  }

  void refresh();
</script>

<aside
  class="activity-sidebar"
  aria-label="Recent activity from people you follow"
  on:mouseenter={onMouseEnter}
  on:mouseleave={onMouseLeave}
>
  <h2 class="activity-title">Activity</h2>
  {#if reeds.length > 0}
    <ul class="activity-list">
      {#each reeds as reed (reed.userID)}
        <li>
          <button type="button" class="activity-row" on:click={() => goto(`/reed/${reed.id}`)}>
            <ReedAuthorHeader
              userID={reed.userID}
              username={authors[reed.userID]?.username ?? reed.userID}
              avatarSize="32px"
              stopPropagation
              linked={false}
            />
            {#if reed.content}
              <p class="activity-preview">{stripMarkdown(reed.content)}</p>
            {/if}
            <p class="activity-timestamp">{formatRelativeTime(reed.serverSignature?.timestamp)}</p>
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="no-activity">
      New reeds from the people you follow will show up here.
    </p>
  {/if}
</aside>

<style>
  .activity-sidebar {
    display: none;
  }

  .no-activity {
    margin: 0;
    padding: 0 0.3rem;
    color: var(--muted);
    font-size: 0.8rem;
  }

  @media (min-width: 1400px) {
    .activity-sidebar {
      display: block;
      position: fixed;
      top: calc(3rem + 1px);
      right: 0;
      bottom: 0;
      width: var(--activity-sidebar-width);
      background: var(--surface);
      border-left: 1px solid var(--border);
      padding: 1rem 0.7rem;
      overflow-y: auto;
      z-index: 90;
    }
  }

  .activity-title {
    margin: 0 0 0.6rem;
    padding: 0 0.3rem;
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .activity-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }

  .activity-row {
    display: block;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    border-radius: 10px;
    padding: 0.5rem 0.3rem;
    font: inherit;
    color: inherit;
    cursor: pointer;
  }

  .activity-row:hover {
    background: var(--input-bg);
  }

  .activity-preview {
    margin: 0.35rem 0 0;
    color: var(--muted);
    font-size: 0.8rem;
    word-break: break-word;
  }

  .activity-timestamp {
    margin: 0.2rem 0 0;
    color: var(--muted);
    font-size: 0.72rem;
  }
</style>
