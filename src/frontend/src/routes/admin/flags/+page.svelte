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
    { value: 'void', label: 'Void' },
  ];

  type Mode = 'pending' | 'dismissed' | 'void';
  const hashMode = $page.url.hash.slice(1);
  let mode: Mode = hashMode === 'dismissed' || hashMode === 'void' ? hashMode : 'pending';
  $: keepInHash($page.url, mode === 'pending' ? '' : `#${mode}`);

  const PAGE = 50;
  const STATES = { pending: 'open', dismissed: 'dismissed', void: 'void' } as const;
  // Read from this device's copies, newest first; the sync keeps them current.
  let cases: Record<Mode, FlagCase[]> = { pending: [], dismissed: [], void: [] };
  let more: Record<Mode, boolean> = { pending: false, dismissed: false, void: false };
  let reeds: Record<string, ReedType | null> = {};
  let authorCounts: Record<string, number> = {};
  let loadError = '';
  let flagsLoaded = false;
  let actingOn: string | null = null;

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: serverID = parseCanonicalId($page.data?.user?.id ?? '')?.[1] ?? '';
  $: shown = cases[mode];

  onMount(() => {
    if (!isAdmin) return;
    void syncAdminRecords();
  });

  $: if (isAdmin) void $adminRecordsChanged, loadFlags().catch((err) => (loadError = err instanceof Error ? err.message : 'Could not load flags.'));

  // Reloads as many cases as are shown, at least a page of each state.
  async function loadFlags(): Promise<void> {
    const modes = Object.keys(STATES) as Mode[];
    const pages = await Promise.all(modes.map((m) => flagsRepository.cases(STATES[m], Math.max(cases[m].length, PAGE) + 1)));
    const next = { ...cases };
    modes.forEach((m, i) => {
      const keep = Math.max(cases[m].length, PAGE);
      more = { ...more, [m]: pages[i].length > keep };
      next[m] = pages[i].slice(0, keep);
    });
    cases = next;
    flagsLoaded = true;
    const all = modes.flatMap((m) => cases[m]);
    fetchContents(all);
    countFlags(all);
  }

  async function loadMore(): Promise<void> {
    const m = mode;
    const page = await flagsRepository.cases(STATES[m], PAGE + 1, cases[m][cases[m].length - 1]);
    more = { ...more, [m]: page.length > PAGE };
    cases = { ...cases, [m]: [...cases[m], ...page.slice(0, PAGE)] };
    fetchContents(page);
    countFlags(page);
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
        {:else if shown.length === 0}
          <div class="empty-state">
            <h3>{mode === 'pending' ? 'No pending flags' : mode === 'dismissed' ? 'No dismissed cases' : 'No void cases'}</h3>
          </div>
        {:else}
          <ul class="flag-list">
            {#each shown as f (f.id)}
              <FlagCaseCard
                flagCase={f}
                reed={reeds[f.reedId]}
                {serverID}
                authorFlagCount={authorCounts[f.authorId]}
                showStates={mode === 'void'}
                on:takeAction={(e) => (actingOn = e.detail)}
              />
            {/each}
          </ul>
          {#if more[mode]}
            <button class="btn secondary more" type="button" on:click={loadMore}>Load more</button>
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
