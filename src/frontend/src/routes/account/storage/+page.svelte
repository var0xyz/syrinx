<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Username from '$lib/components/Username.svelte';
  import StorageUsage from '$lib/components/StorageUsage.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { getStorageQuota } from '$lib/services/pwa';
  import { evictUsers, syncPendingEvictions } from '$lib/services/eviction';
  import { forgetBlock } from '$lib/services/blockedBy';
  import { freeableBytes, listStoredUsers, type StoredUser } from '$lib/services/storedUsers';
  import { federatedServersRepository } from '$lib/repositories/federatedServers';
  import { foreignServerOf } from '$lib/services/peerServers';
  import { formatBytes } from '$lib/utils/bytes';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';

  type SortKey = 'user' | 'server' | 'reeds' | 'visited' | 'size';

  let rows: StoredUser[] = [];
  let storage: { used: number; total: number } | null = null;
  let storageLoaded = false;
  let loading = true;
  let failed = false;
  let failure = '';
  let selected = new Set<string>();
  let sortKey: SortKey = 'size';
  let descending = true;
  // Peer id → name; the server column only shows when there are peers.
  let serverNames = new Map<string, string>();

  const ownServerName = typeof localStorage !== 'undefined' ? localStorage.getItem('serverName') ?? '' : '';

  onMount(async () => {
    try {
      const peers = await federatedServersRepository.all();
      serverNames = new Map(peers.map((peer) => [peer.id, peer.name]));
    } catch (error) {
      console.error('[storage] could not read federated servers', error);
    }
    await load();
  });

  async function loadQuota() {
    storage = await getStorageQuota();
    storageLoaded = true;
  }

  async function load() {
    void loadQuota();
    try {
      rows = await listStoredUsers();
      failed = false;
    } catch (error) {
      console.error('[storage] could not list stored users', error);
      failure = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
      failed = true;
    } finally {
      loading = false;
    }
    const present = new Set(rows.filter((row) => !row.evicting).map((row) => row.userID));
    selected = new Set([...selected].filter((id) => present.has(id)));
  }

  function serverOf(row: StoredUser): string {
    const id = foreignServerOf(row.userID);
    if (!id) return ownServerName;
    return serverNames.get(id) ?? id;
  }

  function nameOf(row: StoredUser): string {
    return row.username ?? row.userID;
  }

  function compare(key: SortKey, a: StoredUser, b: StoredUser): number {
    switch (key) {
      case 'user':
        return nameOf(a).localeCompare(nameOf(b), undefined, { sensitivity: 'base' });
      case 'server':
        return serverOf(a).localeCompare(serverOf(b), undefined, { sensitivity: 'base' });
      case 'reeds':
        return a.reeds - b.reeds;
      case 'visited':
        return (a.lastVisitedAt ?? 0) - (b.lastVisitedAt ?? 0);
      case 'size':
        return a.bytes - b.bytes;
    }
  }

  function sortBy(key: SortKey) {
    if (sortKey === key) {
      descending = !descending;
    } else {
      sortKey = key;
      descending = key !== 'user' && key !== 'server';
    }
  }

  // Sizes sort as raw bytes, so 2 Bytes always ranks below 1 MB.
  $: sorted = [...rows].sort((a, b) => (descending ? -1 : 1) * compare(sortKey, a, b));
  $: showServer = serverNames.size > 0;
  $: selectable = rows.filter((row) => !row.evicting);
  $: allSelected = selectable.length > 0 && selectable.every((row) => selected.has(row.userID));
  $: freeing = rows.filter((row) => selected.has(row.userID)).reduce((sum, row) => sum + freeableBytes(row), 0);

  function toggle(userID: string) {
    const next = new Set(selected);
    if (next.has(userID)) next.delete(userID);
    else next.add(userID);
    selected = next;
  }

  function toggleAll() {
    selected = allSelected ? new Set() : new Set(selectable.map((row) => row.userID));
  }

  async function deleteSelected() {
    const ids = [...selected];
    if (ids.length === 0) return;
    const chosen = new Set(ids);
    rows = rows.map((row) => (chosen.has(row.userID) ? { ...row, evicting: true } : row));
    selected = new Set();
    try {
      // A block of the viewer goes with its keys; their profile fetches it again.
      const blockedYou = new Set(rows.filter((row) => row.blockedYou).map((row) => row.userID));
      for (const id of ids.filter((id) => blockedYou.has(id))) await forgetBlock(id);
      await evictUsers(ids.filter((id) => !blockedYou.has(id)));
    } catch (error) {
      console.error('[storage] could not queue eviction', error);
      notificationStore.error('Could not delete');
    }
    await load();
    // Whatever didn't ack stays queued and retries on reconnect.
    await syncPendingEvictions();
    await load();
  }

  function arrow(key: SortKey, current: SortKey, desc: boolean): string {
    if (key !== current) return '';
    return desc ? '↓' : '↑';
  }
</script>

<Auth>
<SideNav currentPage="account" />
<div class="page">
<div class="stored">
  <h1>Stored users</h1>
  <p class="muted">What this device holds of other people: their profile, keys and
    reeds. Deleting someone frees that space; their reeds come back a page at a
    time when you next open their profile.</p>

  {#if storageLoaded}
    <StorageUsage {storage} {freeing} />
  {/if}

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if failed}
    <p class="muted">Could not read local storage.</p>
    {#if failure}<p class="muted"><code>{failure}</code></p>{/if}
    <button class="btn" on:click={load}>Try again</button>
  {:else if rows.length === 0}
    <p class="muted">You aren't holding anything of anyone else.</p>
  {:else}
    <div class="toolbar">
      <span class="muted">{rows.length} users</span>
      <button class="btn danger" on:click={deleteSelected} disabled={selected.size === 0}>
        Delete selected{selected.size > 0 ? ` (${selected.size})` : ''}
      </button>
    </div>

    <table>
      <thead>
        <tr>
          <th class="check">
            <input type="checkbox" checked={allSelected} disabled={selectable.length === 0} on:change={toggleAll} aria-label="Select all" />
          </th>
          <th><button class="sort" on:click={() => sortBy('user')}>User<span class="arrow">{arrow('user', sortKey, descending)}</span></button></th>
          {#if showServer}
            <th class="server"><button class="sort" on:click={() => sortBy('server')}>Server<span class="arrow">{arrow('server', sortKey, descending)}</span></button></th>
          {/if}
          <th class="num"><button class="sort" on:click={() => sortBy('reeds')}>Reeds<span class="arrow">{arrow('reeds', sortKey, descending)}</span></button></th>
          <th class="visited"><button class="sort" on:click={() => sortBy('visited')}>Last visit<span class="arrow">{arrow('visited', sortKey, descending)}</span></button></th>
          <th class="num"><button class="sort" on:click={() => sortBy('size')}>Size<span class="arrow">{arrow('size', sortKey, descending)}</span></button></th>
        </tr>
      </thead>
      <tbody>
        {#each sorted as row (row.userID)}
          <tr class:evicting={row.evicting}>
            <td class="check">
              <input
                type="checkbox"
                checked={selected.has(row.userID)}
                disabled={row.evicting}
                on:change={() => toggle(row.userID)}
                aria-label="Select {nameOf(row)}"
              />
            </td>
            <td class="who">
              {#if row.username}
                <Username userID={row.userID} username={row.username} at={true} />
              {:else}
                <a class="raw-id" href="/profile/{row.userID}">~{row.userID}</a>
              {/if}
              {#if row.evicting}
                <span class="tag">evicting…</span>
              {:else if row.removed}
                <span class="tag">removed account</span>
              {:else if row.blockedYou}
                <span class="tag">blocked you</span>
              {/if}
            </td>
            {#if showServer}
              <td class="server">{serverOf(row)}</td>
            {/if}
            <td class="num">{row.reeds}</td>
            <td class="visited">{row.lastVisitedAt ? formatRelativeTime(fromUnix(row.lastVisitedAt)) : '—'}</td>
            <td class="num size">
              {formatBytes(row.bytes)}
              {#if row.locked}
                <span class="lock-icon" role="img" aria-label="Profile and keys kept"></span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>

    <p class="muted summary">
      {#if selected.size > 0}
        {selected.size} selected · frees {formatBytes(freeing)}
      {:else}
        Select users to see how much deleting them frees.
      {/if}
    </p>

    {#if rows.some((row) => row.locked)}
      <p class="muted legend">
        <span class="lock-icon" aria-hidden="true"></span>
        Deleting these users removes their reeds but keeps their profile and
        keys, since you follow or verified them
      </p>
    {/if}
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

  .stored {
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

  .toolbar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin: 1.25rem 0 0.5rem;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }

  th,
  td {
    padding: 0.5rem 0.4rem;
    border-top: 1px solid var(--border);
    text-align: left;
    vertical-align: middle;
  }

  th {
    border-top: none;
  }

  .num {
    white-space: nowrap;
  }

  .check {
    width: 1.5rem;
  }

  input[type='checkbox'] {
    width: auto;
    margin: 0;
  }

  .who {
    min-width: 0;
    word-break: break-all;
  }

  .server,
  .visited {
    color: var(--muted);
    white-space: nowrap;
  }

  .sort {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--muted);
    cursor: pointer;
    white-space: nowrap;
    width: auto;
    text-align: left;
  }

  /* Reserved even when empty, so sorting never resizes the header. */
  .arrow {
    display: inline-block;
    width: 1em;
    margin-left: 0.15em;
  }

  .raw-id {
    color: var(--muted);
  }

  .tag {
    margin-left: 0.4rem;
    font-size: 0.7rem;
    color: var(--muted);
    white-space: nowrap;
  }

  .lock-icon {
    display: inline-block;
    flex: none;
    width: 0.9rem;
    height: 0.9rem;
    margin-left: 0.25rem;
    vertical-align: -0.1rem;
    background-color: currentColor;
    -webkit-mask: url('/icons/lock-16.png') center / contain no-repeat;
    mask: url('/icons/lock-16.png') center / contain no-repeat;
  }

  .legend {
    display: flex;
    gap: 0.4rem;
    align-items: flex-start;
    font-size: 0.8rem;
  }

  .legend .lock-icon {
    margin-left: 0;
  }

  .summary {
    margin-top: 0.75rem;
  }

  tr.evicting td {
    opacity: 0.45;
  }

  .btn {
    flex: none;
    width: auto;
    white-space: nowrap;
    padding: 0.35rem 0.7rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    cursor: pointer;
    font-size: 0.8rem;
  }

  .btn.danger {
    background: var(--error, #e03131);
    border-color: var(--error, #e03131);
    color: #fff;
    font-weight: 600;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  @media (max-width: 600px) {
    .visited {
      display: none;
    }
  }
</style>
