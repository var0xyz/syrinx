<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import BlockButton from '$lib/components/BlockButton.svelte';
  import { blocksRepository, pendingBlocksRepository, pendingUnblocksRepository } from '$lib/repositories/blocks';
  import { blocksChanged, reconcileBlocks } from '$lib/services/blocks';
  import { userRepository } from '$lib/repositories/user';

  type Row = { userID: string; username: string; blockedAt: string | null };

  let rows: Row[] = [];
  let loading = true;

  /** Confirmed blocks newest first, then ones still waiting to be sent;
   * a block with a lift queued is already gone from this list. */
  async function load() {
    const confirmed = await blocksRepository.getAll();
    const pending = await pendingBlocksRepository.getAll();
    const lifting = new Set((await pendingUnblocksRepository.getAll()).map((r) => r.blockedUserID));
    const ids = new Map<string, string | null>();
    for (const cert of confirmed) {
      if (!lifting.has(cert.blockedUserID)) ids.set(cert.blockedUserID, cert.serverSignature.timestamp);
    }
    for (const record of pending) if (!ids.has(record.blockedUserID)) ids.set(record.blockedUserID, null);

    const next: Row[] = [];
    for (const [userID, blockedAt] of ids) {
      const profile = await userRepository.get(userID).catch(() => null);
      next.push({ userID, username: profile?.username ?? '', blockedAt });
    }
    next.sort((a, b) => (b.blockedAt ?? '9').localeCompare(a.blockedAt ?? '9'));
    rows = next;
    loading = false;
  }

  onMount(() => {
    void load();
    reconcileBlocks().catch((error) => console.warn('[blocked] could not reconcile blocks', error));
  });
  $: if ($blocksChanged > 0) void load();

  function day(timestamp: string): string {
    return new Date(timestamp).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
  }
</script>

<Auth>
<SideNav currentPage="account" />
<div class="page">
<div class="blocked">
  <h1>Blocked users</h1>
  <p class="muted">They can't see your profile or reeds. Unblocking lets them back in, but
    doesn't restore their follow or anything they had to delete.</p>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if rows.length === 0}
    <p class="muted">You haven't blocked anyone.</p>
  {:else}
    <ul>
      {#each rows as row (row.userID)}
        <li>
          <a class="who" href="/profile/{row.userID}">
            <Avatar userID={row.userID} username={row.username} />
            <span class="names">
              {#if row.username}<span class="username">{row.username}</span>{/if}
              <span class="id">~{row.userID}</span>
            </span>
          </a>
          <span class="when muted">{row.blockedAt ? day(row.blockedAt) : 'sending…'}</span>
          <BlockButton
            userID={row.userID}
            username={row.username}
            unblockOnly
            on:unblocked={() => (rows = rows.filter((r) => r.userID !== row.userID))}
          />
        </li>
      {/each}
    </ul>
  {/if}
</div>
<BottomToolbar currentPage="account" />
</div>
</Auth>

<style>
  .page {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  @media (min-width: 768px) {
    .page {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .page {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .blocked {
    flex: 1;
    width: 100%;
    max-width: 44rem;
    margin: 0 auto;
    padding: 1.5rem 1rem 4rem;
  }

  h1 {
    font-size: 1.35rem;
    margin: 0 0 0.5rem;
  }

  .muted {
    color: var(--muted);
    font-size: 0.9rem;
  }

  ul {
    list-style: none;
    margin: 1.25rem 0 0;
    padding: 0;
  }

  li {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 0;
    border-bottom: 1px solid var(--border);
  }

  li:last-child {
    border-bottom: none;
  }

  .who {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex: 1;
    min-width: 0;
    color: var(--fg);
    text-decoration: none;
  }

  .names {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .username {
    font-weight: 600;
  }

  .id {
    color: var(--muted);
    font-size: 0.8rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .when {
    flex: none;
    white-space: nowrap;
  }
</style>
