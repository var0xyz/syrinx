<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import AdminActionsModal from '$lib/components/AdminActionsModal.svelte';
  import FlagCaseCard from '$lib/components/FlagCaseCard.svelte';
  import type { ReedType } from '$lib/types/reed';
  import { adminRecordsChanged, syncAdminRecords } from '$lib/services/admin';
  import { flagsRepository, type FlagCase } from '$lib/repositories/flags';
  import { serverConnection } from '$lib/services/serverConnection';
  import { keepInHash } from '$lib/utils/hashMode';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { adminPermissions, adminTabs, canMail } from '$lib/stores/adminPermissions';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  const modeOptions = [
    { value: 'pending', label: 'Pending' },
    { value: 'dismissed', label: 'Dismissed' },
    { value: 'withdrawn', label: 'Withdrawn' },
  ];

  type Mode = 'pending' | 'dismissed' | 'withdrawn';
  const hashMode = $page.url.hash.slice(1);
  let mode: Mode = hashMode === 'dismissed' || hashMode === 'withdrawn' ? hashMode : 'pending';
  $: keepInHash($page.url, mode === 'pending' ? '' : `#${mode}`);

  const PAGE = 50;
  // Read from this device's copies, newest first; the sync keeps them current.
  let flags: FlagCase[] = [];
  let dismissed: FlagCase[] = [];
  let withdrawn: FlagCase[] = [];
  let morePending = false;
  let moreDismissed = false;
  let moreWithdrawn = false;
  let reeds: Record<string, ReedType | null> = {};
  let authorCounts: Record<string, number> = {};
  let loadError = '';
  let flagsLoaded = false;
  let actingOn: string | null = null;

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: serverID = parseCanonicalId($page.data?.user?.id ?? '')?.[1] ?? '';

  onMount(() => {
    if (!isAdmin) return;
    void syncAdminRecords();
  });

  $: if (isAdmin) void $adminRecordsChanged, loadFlags().catch((err) => (loadError = err instanceof Error ? err.message : 'Could not load flags.'));

  // Reloads as many cases as are shown, at least a page of each.
  async function loadFlags(): Promise<void> {
    const [open, done, recalled] = await Promise.all([
      flagsRepository.openCases(Math.max(flags.length, PAGE) + 1),
      flagsRepository.dismissedCases(Math.max(dismissed.length, PAGE) + 1),
      flagsRepository.withdrawnCases(Math.max(withdrawn.length, PAGE) + 1),
    ]);
    morePending = open.length > Math.max(flags.length, PAGE);
    moreDismissed = done.length > Math.max(dismissed.length, PAGE);
    moreWithdrawn = recalled.length > Math.max(withdrawn.length, PAGE);
    flags = open.slice(0, Math.max(flags.length, PAGE));
    dismissed = done.slice(0, Math.max(dismissed.length, PAGE));
    withdrawn = recalled.slice(0, Math.max(withdrawn.length, PAGE));
    flagsLoaded = true;
    fetchContents([...flags, ...dismissed, ...withdrawn]);
    countFlags([...flags, ...dismissed, ...withdrawn]);
  }

  async function loadMorePending(): Promise<void> {
    const last = flags[flags.length - 1];
    const latestAt = Math.max(...last.flags.map((f) => f.serverSignature?.signedAt ?? 0));
    const more = await flagsRepository.openCases(PAGE + 1, { reedId: last.reedId, latestAt });
    morePending = more.length > PAGE;
    flags = [...flags, ...more.slice(0, PAGE)];
    fetchContents(more);
    countFlags(more);
  }

  async function loadMoreDismissed(): Promise<void> {
    const more = await flagsRepository.dismissedCases(PAGE + 1, dismissed[dismissed.length - 1].dismissal);
    moreDismissed = more.length > PAGE;
    dismissed = [...dismissed, ...more.slice(0, PAGE)];
    fetchContents(more);
    countFlags(more);
  }

  async function loadMoreWithdrawn(): Promise<void> {
    const more = await flagsRepository.withdrawnCases(PAGE + 1, withdrawn[withdrawn.length - 1].flags[0]);
    moreWithdrawn = more.length > PAGE;
    withdrawn = [...withdrawn, ...more.slice(0, PAGE)];
    fetchContents(more);
    countFlags(more);
  }

  function fetchContents(cases: FlagCase[]): void {
    for (const f of cases) {
      if (f.reedId in reeds) continue;
      reeds = { ...reeds, [f.reedId]: null };
      serverConnection
        .requestReedContent(f.reedId)
        .then((reed) => (reeds = { ...reeds, [f.reedId]: reed }))
        .catch(() => {});
    }
  }

  /** Counts every flag on each author's reeds this device holds. */
  function countFlags(reeds: FlagCase[]): void {
    for (const authorId of new Set(reeds.map((f) => f.authorId))) {
      flagsRepository
        .historyOf(authorId)
        .then((h) => (authorCounts = { ...authorCounts, [authorId]: h.count }))
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
      {:else}
        <div class="mode-picker">
          <ModeSwitch options={modeOptions} bind:value={mode} />
        </div>

        {#if loadError}
          <p class="error" role="alert">{loadError}</p>
        {:else if !flagsLoaded}
          <!-- Nothing to show until this device's copies are read. -->
        {:else if mode === 'pending'}
          {#if flags.length === 0}
            <div class="empty-state">
              <h3>No pending flags</h3>
            </div>
          {:else}
            <ul class="flag-list">
              {#each flags as f (f.reedId)}
                <FlagCaseCard
                  flagCase={f}
                  reed={reeds[f.reedId]}
                  {serverID}
                  authorFlagCount={authorCounts[f.authorId]}
                  on:takeAction={(e) => (actingOn = e.detail)}
                />
              {/each}
            </ul>
            {#if morePending}
              <button class="btn secondary more" type="button" on:click={loadMorePending}>Load more</button>
            {/if}
          {/if}
        {:else if mode === 'withdrawn'}
          {#if withdrawn.length === 0}
            <div class="empty-state">
              <h3>No withdrawn flags</h3>
            </div>
          {:else}
            <ul class="flag-list">
              {#each withdrawn as f (`${f.reedId} ${f.flags[0].reporterId}`)}
                <FlagCaseCard
                  flagCase={f}
                  reed={reeds[f.reedId]}
                  {serverID}
                  authorFlagCount={authorCounts[f.authorId]}
                  showStates
                />
              {/each}
            </ul>
            {#if moreWithdrawn}
              <button class="btn secondary more" type="button" on:click={loadMoreWithdrawn}>Load more</button>
            {/if}
          {/if}
        {:else if dismissed.length === 0}
          <div class="empty-state">
            <h3>No dismissed flags</h3>
          </div>
        {:else}
          <ul class="flag-list">
            {#each dismissed as f (f.dismissal?.id)}
              <FlagCaseCard flagCase={f} reed={reeds[f.reedId]} {serverID} authorFlagCount={authorCounts[f.authorId]} />
            {/each}
          </ul>
          {#if moreDismissed}
            <button class="btn secondary more" type="button" on:click={loadMoreDismissed}>Load more</button>
          {/if}
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

  .mode-picker {
    display: flex;
    justify-content: center;
    margin-bottom: 1rem;
  }

  .flag-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .more {
    margin-top: 0.75rem;
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

  .btn.secondary {
    background: transparent;
    color: var(--fg);
    border: 1px solid var(--border);
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
