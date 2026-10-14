<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import AdminActionsModal from '$lib/components/AdminActionsModal.svelte';
  import Username from '$lib/components/Username.svelte';
  import { apiService } from '$lib/services/api';
  import { serverConnection } from '$lib/services/serverConnection';
  import { keepInHash } from '$lib/utils/hashMode';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { adminPermissions, adminTabs, canMail } from '$lib/stores/adminPermissions';
  import type * as api from '$lib/types/api';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  const modeOptions = [
    { value: 'pending', label: 'Pending' },
    { value: 'dismissed', label: 'Dismissed' },
  ];

  let mode: 'pending' | 'dismissed' = $page.url.hash === '#dismissed' ? 'dismissed' : 'pending';
  $: keepInHash($page.url, mode === 'dismissed' ? '#dismissed' : '');

  let flags: api.FlaggedReed[] = [];
  let dismissed: api.FlaggedReed[] = [];
  let dismissedCursor = '';
  const MAX_DISMISS_REASON = 140;
  // One open dismissal form at a time.
  let dismissing: string | null = null;
  let dismissReason = '';
  let dismissError = '';
  let dismissSubmitting = false;
  let contents: Record<string, string | null> = {};
  let loadError = '';
  let flagsLoaded = false;
  let actingOn: string | null = null;

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: serverID = parseCanonicalId($page.data?.user?.id ?? '')?.[1] ?? '';

  onMount(() => {
    if (!isAdmin) return;
    void loadFlags().catch((err) => (loadError = err instanceof Error ? err.message : 'Could not load flags.'));
  });

  async function loadFlags(): Promise<void> {
    const [open, done] = await Promise.all([apiService.listFlags('open'), apiService.listFlags('dismissed')]);
    flags = open.reeds;
    dismissed = done.reeds;
    dismissedCursor = done.nextCursor;
    flagsLoaded = true;
    fetchContents([...flags, ...dismissed]);
  }

  async function loadMoreDismissed(): Promise<void> {
    const more = await apiService.listFlags('dismissed', dismissedCursor);
    dismissed = [...dismissed, ...more.reeds];
    dismissedCursor = more.nextCursor;
    fetchContents(more.reeds);
  }

  function fetchContents(reeds: api.FlaggedReed[]): void {
    for (const f of reeds) {
      if (f.reedId in contents) continue;
      contents = { ...contents, [f.reedId]: null };
      serverConnection
        .requestReedContent(f.reedId)
        .then((reed) => (contents = { ...contents, [f.reedId]: reed.content ?? '' }))
        .catch(() => {});
    }
  }

  function openDismiss(reedId: string): void {
    dismissing = reedId;
    dismissReason = '';
    dismissError = '';
  }

  async function submitDismiss(f: api.FlaggedReed): Promise<void> {
    dismissSubmitting = true;
    dismissError = '';
    try {
      const dismissal = await apiService.dismissFlags(f.reedId, dismissReason.trim());
      flags = flags.filter((open) => open.reedId !== f.reedId);
      dismissed = [{ ...f, dismissal }, ...dismissed];
      dismissing = null;
    } catch (err) {
      dismissError = err instanceof Error ? err.message : 'Could not dismiss these flags.';
    } finally {
      dismissSubmitting = false;
    }
  }

  /** The server a remote reporter belongs to, null for a local one. */
  function remoteServer(userID: string): string | null {
    const server = parseCanonicalId(userID)?.[1];
    return server && server !== serverID ? server : null;
  }

  function reedHref(reedId: string): string {
    return `/reed/${reedId}`;
  }
</script>

{#snippet flagCard(f: api.FlaggedReed)}
  <li class="flag">
    <div class="flag-head">
      <span class="author name"><Username userID={f.authorId} color="var(--primary)" /></span>
      <span class="report-count">
        {f.flags.length} {f.flags.length === 1 ? 'flag' : 'flags'}
      </span>
    </div>

    {#if contents[f.reedId]}
      <blockquote class="content">{contents[f.reedId]}</blockquote>
    {:else}
      <p class="content loading">Retrieving from the network…</p>
    {/if}

    <ul class="reports">
      {#each f.flags as rep (rep.reporterId)}
        {@const server = remoteServer(rep.reporterId)}
        <li>
          <span class="name"><Username userID={rep.reporterId} color="var(--primary)" /></span>{#if server}<span class="server">({server})</span>{/if}
          {#if rep.note}<span class="note">“{rep.note}”</span>{/if}
          <span class="report-when">{formatRelativeTime(fromUnix(rep.createdAt) ?? new Date())}</span>
        </li>
      {/each}
    </ul>

    {#if f.dismissal}
      <blockquote class="reason">{f.dismissal.reason}</blockquote>
      <p class="meta">
        Dismissed by
        {#if f.dismissal.dismissedBy}
          <span class="name"><Username userID={f.dismissal.dismissedBy} color="var(--primary)" /></span>
        {:else}
          a removed account
        {/if}
        · {formatRelativeTime(fromUnix(f.dismissal.dismissedAt) ?? new Date())}
      </p>
      <div class="flag-actions">
        <a class="btn secondary" href={reedHref(f.reedId)}>Open reed</a>
      </div>
    {:else if dismissing === f.reedId}
      <div class="decision-form">
        <label class="dismiss-field">
          <span>Why are you dismissing these flags?</span>
          <textarea bind:value={dismissReason} rows="3" maxlength={MAX_DISMISS_REASON}></textarea>
          <span class="character-counter">{dismissReason.length}/{MAX_DISMISS_REASON} characters</span>
        </label>
        {#if dismissError}<p class="decision-error" role="alert">{dismissError}</p>{/if}
        <div class="decision-actions">
          <button class="btn secondary" type="button" on:click={() => (dismissing = null)}>Cancel</button>
          <button
            class="btn primary"
            type="button"
            disabled={dismissSubmitting || !dismissReason.trim()}
            on:click={() => submitDismiss(f)}
          >
            Dismiss
          </button>
        </div>
      </div>
    {:else}
      <div class="flag-actions">
        <a class="btn secondary" href={reedHref(f.reedId)}>Open reed</a>
        <button class="btn secondary" type="button" on:click={() => (actingOn = f.authorId)}>
          Take action
        </button>
        <button class="btn secondary dismiss" type="button" on:click={() => openDismiss(f.reedId)}>
          Dismiss
        </button>
      </div>
    {/if}
  </li>
{/snippet}

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
          <!-- Flags come from the server; nothing to show until then. -->
        {:else if mode === 'pending'}
          {#if flags.length === 0}
            <div class="empty-state">
              <h3>No pending flags</h3>
            </div>
          {:else}
            <ul class="flag-list">
              {#each flags as f (f.reedId)}
                {@render flagCard(f)}
              {/each}
            </ul>
          {/if}
        {:else if dismissed.length === 0}
          <div class="empty-state">
            <h3>No dismissed flags</h3>
          </div>
        {:else}
          <ul class="flag-list">
            {#each dismissed as f (f.dismissal?.id)}
              {@render flagCard(f)}
            {/each}
          </ul>
          {#if dismissedCursor}
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

  .flag {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    overflow-wrap: anywhere;
  }

  .flag-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .author {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .server {
    margin-left: 0.25rem;
    color: var(--muted);
    font-weight: 500;
  }

  .report-count {
    white-space: nowrap;
    flex-shrink: 0;
    font-size: 0.75rem;
    font-weight: 700;
    padding: 0.25rem 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: #c0392b;
  }

  .content {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .content.loading {
    color: var(--muted);
    font-style: italic;
  }

  .reports {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .note {
    margin-left: 0.25rem;
  }

  .report-when {
    margin-left: 0.5rem;
  }

  .flag-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 0.25rem;
  }

  .dismiss {
    margin-left: auto;
  }

  .name {
    color: var(--primary);
    font-weight: 600;
  }

  .reason {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .meta {
    margin: 0;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .decision-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.25rem;
  }

  .decision-error {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .more {
    margin-top: 0.75rem;
  }

  a.btn {
    text-decoration: none;
    display: inline-flex;
    align-items: center;
  }

  .decision-form {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding-top: 0.5rem;
    border-top: 1px solid var(--border);
  }

  .dismiss-field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .dismiss-field textarea {
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.9rem;
    resize: vertical;
  }

  .character-counter {
    text-align: right;
    font-size: 0.8rem;
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

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
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
