<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import AdminActionsModal from '$lib/components/AdminActionsModal.svelte';
  import FlagCaseCard from '$lib/components/FlagCaseCard.svelte';
  import type { ReedType } from '$lib/types/reed';
  import Username from '$lib/components/Username.svelte';
  import { adminRecordsChanged, syncAdminRecords } from '$lib/services/admin';
  import { flagsRepository, type FlagHistory } from '$lib/repositories/flags';
  import { serverConnection } from '$lib/services/serverConnection';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { adminPermissions, adminTabs, canMail } from '$lib/stores/adminPermissions';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));
  $: authorID = $page.params.userID;
  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: isOwn = authorID === $page.data?.user?.id;
  $: serverID = parseCanonicalId($page.data?.user?.id ?? '')?.[1] ?? '';

  // Read from this device's copies; the sync keeps them current.
  let history: FlagHistory | null = null;
  let reeds: Record<string, ReedType | null> = {};
  let loadError = '';
  let actingOn: string | null = null;

  onMount(() => {
    if (isAdmin) void syncAdminRecords();
  });

  $: if (isAdmin && !isOwn && authorID) void $adminRecordsChanged, load(authorID);

  async function load(id: string): Promise<void> {
    try {
      history = await flagsRepository.historyOf(id);
    } catch (err) {
      loadError = err instanceof Error ? err.message : 'Could not load flags.';
      return;
    }
    for (const c of history.cases) {
      if (c.reedId in reeds) continue;
      reeds = { ...reeds, [c.reedId]: null };
      serverConnection
        .requestReedContent(c.reedId)
        .then((reed) => (reeds = { ...reeds, [c.reedId]: reed }))
        .catch(() => {});
    }
  }
</script>

<Auth>
  <SideNav currentPage="admin" />
  <div class="admin-container">
    <SectionTabs {tabs} active="flags" />
    <div class="admin-content">
      {#if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else if isOwn}
        <p class="error" role="alert">You can't see the flags on your own reeds.</p>
      {:else}
        <h2 class="title">
          Flags received by <span class="name"><Username userID={authorID} color="var(--primary)" /></span>
        </h2>
        {#if loadError}
          <p class="error" role="alert">{loadError}</p>
        {:else if !history}
          <!-- Nothing to show until this device's copies are read. -->
        {:else if history.cases.length === 0}
          <div class="empty-state">
            <h3>No flags on their reeds</h3>
          </div>
        {:else}
          <p class="summary">
            {history.count} {history.count === 1 ? 'flag' : 'flags'}, open or dismissed, not counting withdrawn ones
          </p>
          <ul class="flag-list">
            {#each history.cases as c (c.dismissal?.id ?? c.reedId)}
              <FlagCaseCard
                flagCase={c}
                reed={reeds[c.reedId]}
                {serverID}
                showStates
                on:takeAction={(e) => (actingOn = e.detail)}
              />
            {/each}
          </ul>
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>

  {#if actingOn}
    <AdminActionsModal open userID={actingOn} on:close={() => (actingOn = null)} />
  {/if}
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
  }

  .title {
    margin: 0 0 0.5rem 0;
    font-size: 1.1rem;
    overflow-wrap: anywhere;
  }

  .name {
    color: var(--primary);
  }

  .summary {
    margin: 0 0 1rem 0;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .flag-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .error {
    color: #b91c1c;
  }
</style>
