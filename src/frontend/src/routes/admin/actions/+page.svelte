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
  import { cryptoService } from '$lib/services/crypto';
  import { serverConnection } from '$lib/services/serverConnection';
  import {
    ACTION_PERMISSION,
    adminRequestsChanged,
    decideAdminRequest,
    decisionBlocker,
    syncClosedAdminRequests,
  } from '$lib/services/admin';
  import { adminRequestsRepository } from '$lib/repositories/adminRequests';
  import { parsePermissionSet } from '$lib/utils/permissions';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { adminPermissions, adminTabs, canMail, holds } from '$lib/stores/adminPermissions';
  import type * as api from '$lib/types/api';

  type Decision = 'approve' | 'reject' | 'withdraw';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  const modeOptions = [
    { value: 'requests', label: 'Requests' },
    { value: 'flagged', label: 'Flagged' },
  ];

  const actionLabel: Record<string, string> = {
    suspend: 'Suspend',
    reinstate: 'Reinstate',
    kick: 'Kick',
    promote: 'Promote',
    demote: 'Demote',
    permissions: 'Permissions',
    rebind: 'Key rebind',
    broadcast: 'Broadcast',
    message: 'Message',
  };

  const stateLabel: Record<string, string> = {
    pending: 'Pending',
    executed: 'Executed',
    rejected: 'Rejected',
    withdrawn: 'Withdrawn',
    moot: 'Moot',
  };

  const decisionLabel: Record<Decision, string> = { approve: 'Approve', reject: 'Reject', withdraw: 'Withdraw' };

  let mode: 'requests' | 'flagged' = 'requests';
  let pending: api.AdminRequest[] = [];
  let pendingLoaded = false;
  let pendingError = '';
  let closed: api.AdminRequest[] = [];
  const CLOSED_PAGE = 50;
  let closedShown = CLOSED_PAGE;
  let flags: api.FlaggedReed[] = [];
  let contents: Record<string, string | null> = {};
  let fingerprints: Record<string, string> = {};
  let loadError = '';
  let actingOn: string | null = null;

  // One open confirmation at a time.
  let deciding: { id: string; decision: Decision } | null = null;
  let decisionError = '';
  let submitting = false;

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: viewerID = $page.data?.user?.id ?? '';
  $: serverID = parseCanonicalId(viewerID)?.[1] ?? '';
  // The permissions read at app load; the server checks every decision.
  $: viewer = {
    role: $page.data?.user?.role ?? '',
    permissions: Object.values(ACTION_PERMISSION).filter((p) => holds($page.data?.user, $adminPermissions, p)),
  };

  onMount(() => {
    if (!isAdmin) return;
    void loadClosed();
    void loadFlags().catch((err) => (loadError = err instanceof Error ? err.message : 'Could not load flags.'));
  });

  $: $adminRequestsChanged, isAdmin && void loadRequests();

  // Open requests are always read from the server: deciding needs it anyway.
  async function loadRequests(): Promise<void> {
    try {
      pending = (await apiService.listAdminRequests('pending')).requests;
      pendingError = '';
    } catch (err) {
      pendingError = err instanceof Error ? err.message : 'Could not load open requests.';
      return;
    } finally {
      pendingLoaded = true;
    }
    fingerprintsFor(pending);
    await syncClosedAdminRequests(pending.map((r) => r.id)).catch((err) => console.error('Closed request sync failed:', err));
    await loadClosed();
  }

  // Closed requests never change, so they come from the local copy.
  async function loadClosed(): Promise<void> {
    closed = await adminRequestsRepository.getAll();
    fingerprintsFor(closed);
  }

  function fingerprintsFor(reqs: api.AdminRequest[]): void {
    for (const r of reqs) {
      if (r.action === 'rebind' && !fingerprints[r.id]) {
        cryptoService.fingerprintOf(r.body).then((fp) => (fingerprints = { ...fingerprints, [r.id]: grouped(fp) })).catch(() => {});
      }
    }
  }

  async function loadFlags(): Promise<void> {
    flags = (await apiService.listFlags()).reeds;
    for (const f of flags) {
      if (f.reedId in contents) continue;
      contents = { ...contents, [f.reedId]: null };
      serverConnection
        .requestReedContent(f.reedId)
        .then((reed) => (contents = { ...contents, [f.reedId]: reed.content ?? '' }))
        .catch(() => {});
    }
  }

  async function dismiss(reedId: string): Promise<void> {
    await apiService.dismissFlags(reedId);
    flags = flags.filter((f) => f.reedId !== reedId);
  }

  function grouped(fingerprint: string): string {
    return (fingerprint.toUpperCase().match(/.{1,4}/g) ?? []).join(' ');
  }

  function remaining(r: api.AdminRequest): string[] {
    return r.targets.filter((t) => !r.droppedTargets.includes(t));
  }

  function summary(r: api.AdminRequest): string {
    const n = remaining(r).length;
    const accounts = n === 1 ? '1 account' : `${n} accounts`;
    switch (r.action) {
      case 'kick':
        return `Removes ${accounts} and all their content. This can't be undone.`;
      case 'suspend':
        return `Blocks ${accounts} from signing in until reinstated. Peers see no change.`;
      case 'reinstate':
        return `Lifts the suspension on ${accounts}.`;
      case 'promote':
        return 'Makes this user an admin with the permissions below.';
      case 'permissions':
        return 'Replaces this admin’s permissions with the ones below.';
      case 'demote':
        return 'Makes this admin a regular user and revokes their unclaimed admin invites.';
      case 'rebind':
        return 'Revokes their current key as lost and binds the uploaded key, restricted to a rotation.';
      case 'broadcast':
        return 'Sends this message to every local member’s mailbox.';
      case 'message':
        return 'Sends this message privately to their mailbox. It stays out of the ledger.';
    }
    return '';
  }

  function closedAt(r: api.AdminRequest): Date | undefined {
    return fromUnix(r.serverSignature?.signedAt ?? r.createdAt);
  }

  function openDecision(id: string, decision: Decision): void {
    deciding = { id, decision };
    decisionError = '';
  }

  async function submitDecision(r: api.AdminRequest): Promise<void> {
    if (!deciding) return;
    submitting = true;
    decisionError = '';
    try {
      await decideAdminRequest(r, deciding.decision);
      deciding = null;
    } catch (err) {
      decisionError = err instanceof Error ? err.message : 'The decision was refused.';
    } finally {
      submitting = false;
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

{#snippet requestCard(r: api.AdminRequest)}
  {@const blocker = decisionBlocker(r, viewer, viewerID)}
  {@const own = r.requesterId === viewerID}
  {@const permissions = r.action === 'promote' || r.action === 'permissions' ? parsePermissionSet(r.body) : null}
  <li class="request" data-action={r.action}>
    <div class="request-head">
      <span class="action-badge" data-action={r.action}>{actionLabel[r.action] ?? r.action}</span>
      {#if r.state !== 'pending'}
        <span class="state-badge" data-state={r.state}>{stateLabel[r.state] ?? r.state}</span>
      {/if}
      {#if r.action === 'message'}<span class="state-badge">Private</span>{/if}
    </div>

    {#if r.targets.length > 0}
      <p class="targets">
        {#each remaining(r) as t, i (t)}{#if i > 0}, {/if}<span class="name"><Username userID={t} color="var(--primary)" /></span>{/each}{#each r.droppedTargets as t (t)}, <span class="name dropped" title="Left before approval"><Username userID={t} linked={false} color="var(--muted)" /></span>{/each}
      </p>
    {:else}
      <p class="targets everyone">Every local member</p>
    {/if}

    {#if r.action === 'broadcast' || r.action === 'message'}
      <blockquote class="reason">{r.body}</blockquote>
    {:else}
      <blockquote class="reason">{r.reason}</blockquote>
    {/if}

    {#if permissions}
      <ul class="permissions">
        {#each permissions as p (p)}<li>{p}</li>{:else}<li class="none">No permissions</li>{/each}
      </ul>
    {/if}

    {#if r.action === 'rebind'}
      <div class="fingerprint">
        <span class="meta">New key fingerprint</span>
        <code>{fingerprints[r.id] ?? '…'}</code>
      </div>
    {/if}

    <p class="meta">
      Requested by <span class="name"><Username userID={r.requesterId} color="var(--primary)" /></span>
      {formatRelativeTime(fromUnix(r.createdAt) ?? new Date())}
    </p>
    {#if r.state !== 'pending'}
      {@const when = formatRelativeTime(closedAt(r) ?? new Date())}
      <p class="meta">
        {#if (r.state === 'executed' || r.state === 'rejected') && r.decision}
          {r.state === 'executed' ? 'Approved' : 'Rejected'} by
          <span class="name"><Username userID={r.decision.deciderId} color="var(--primary)" /></span>
          {when}
        {:else if r.state === 'withdrawn'}
          Withdrawn {when}
        {:else if r.state === 'moot'}
          Closed by the server {when}: every target left
        {:else}
          {stateLabel[r.state] ?? r.state} {when}
        {/if}
      </p>
    {/if}

    {#if r.state === 'pending'}
      {#if deciding?.id === r.id}
        <div class="decision-form">
          {#if deciding.decision === 'approve'}
            <p class="decision-summary">{summary(r)}</p>
          {/if}
          {#if decisionError}<p class="decision-error" role="alert">{decisionError}</p>{/if}
          <div class="decision-actions">
            <button class="btn secondary" type="button" on:click={() => (deciding = null)}>Cancel</button>
            <button
              class="btn"
              class:primary={deciding.decision === 'approve'}
              class:danger={deciding.decision !== 'approve'}
              type="button"
              disabled={submitting}
              on:click={() => submitDecision(r)}
            >
              Sign and {decisionLabel[deciding.decision].toLowerCase()}
            </button>
          </div>
        </div>
      {:else}
        <div class="request-actions">
          {#if own}
            <button class="btn secondary" type="button" on:click={() => openDecision(r.id, 'withdraw')}>
              Withdraw
            </button>
          {/if}
          {#if !own || viewer.role === 'root'}
            <button class="btn secondary" type="button" disabled={!!blocker} on:click={() => openDecision(r.id, 'reject')}>
              Reject
            </button>
            <button class="btn primary" type="button" disabled={!!blocker} on:click={() => openDecision(r.id, 'approve')}>
              Approve
            </button>
          {/if}
          {#if blocker && !own}
            <span class="ineligible">{blocker}</span>
          {/if}
        </div>
      {/if}
    {/if}
  </li>
{/snippet}

<Auth>
  <SideNav currentPage="admin" />
  <div class="admin-container">
    <SectionTabs {tabs} active="actions" />
    <div class="admin-content">
      {#if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        <div class="mode-picker">
          <ModeSwitch options={modeOptions} bind:value={mode} />
        </div>

        {#if mode === 'requests'}
          {#if pendingError}
            <p class="error" role="alert">{pendingError}</p>
          {:else if !pendingLoaded}
            <!-- Open requests come from the server; nothing to show until then. -->
          {:else if pending.length === 0}
            <div class="empty-state">
              <div class="empty-icon">🛡️</div>
              <h3>Nothing to review</h3>
            </div>
          {:else}
            <ul class="request-list">
              {#each pending as r (r.id)}
                {@render requestCard(r)}
              {/each}
            </ul>
          {/if}

          {#if closed.length > 0}
            <h2 class="section-title">Closed</h2>
            <ul class="request-list">
              {#each closed.slice(0, closedShown) as r (r.id)}
                {@render requestCard(r)}
              {/each}
            </ul>
            {#if closed.length > closedShown}
              <button class="btn secondary more" type="button" on:click={() => (closedShown += CLOSED_PAGE)}>Load more</button>
            {/if}
          {/if}
        {:else if loadError}
          <p class="error" role="alert">{loadError}</p>
        {:else if flags.length === 0}
          <div class="empty-state">
            <div class="empty-icon">🚩</div>
            <h3>No open flags</h3>
          </div>
        {:else}
          <ul class="flag-list">
            {#each flags as f (f.reedId)}
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

                <div class="flag-actions">
                  <a class="btn secondary" href={reedHref(f.reedId)}>Open reed</a>
                  <button class="btn secondary" type="button" on:click={() => (actingOn = f.authorId)}>
                    Act on <span class="name"><Username userID={f.authorId} linked={false} color="var(--primary)" /></span>
                  </button>
                  <button class="btn secondary dismiss" type="button" on:click={() => dismiss(f.reedId)}>
                    Dismiss
                  </button>
                </div>
              </li>
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








  .mode-picker {
    display: flex;
    justify-content: center;
    margin-bottom: 1rem;
  }

  .section-title {
    margin: 1.5rem 0 0.75rem;
    color: var(--muted);
    font-size: 0.85rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
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

  .request-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .request {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .request[data-action='kick'] {
    border-left: 3px solid #c0392b;
  }

  .request-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .action-badge,
  .state-badge {
    font-size: 0.75rem;
    font-weight: 700;
    padding: 0.25rem 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: var(--fg);
  }

  .action-badge[data-action='kick'] {
    color: #c0392b;
  }

  .action-badge[data-action='suspend'],
  .action-badge[data-action='rebind'] {
    color: #d68910;
  }

  .action-badge[data-action='promote'],
  .action-badge[data-action='reinstate'] {
    color: #27ae60;
  }

  .action-badge[data-action='broadcast'] {
    color: var(--primary);
  }

  .state-badge {
    font-weight: 600;
    color: var(--muted);
  }

  .state-badge[data-state='executed'] {
    color: #27ae60;
  }

  .targets {
    margin: 0;
    color: var(--muted);
    overflow-wrap: anywhere;
  }

  .name {
    color: var(--primary);
    font-weight: 600;
  }

  .name.dropped {
    color: var(--muted);
    text-decoration: line-through;
    font-weight: 500;
  }

  .reason {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
  }


  .fingerprint {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .fingerprint code {
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  .meta {
    margin: 0;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .request-actions,
  .decision-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.25rem;
  }

  .decision-actions {
    justify-content: flex-end;
  }

  .decision-error {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .permissions {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  .permissions li {
    padding: 0 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 600;
  }

  .more {
    margin-top: 0.75rem;
  }

  a.btn {
    text-decoration: none;
    display: inline-flex;
    align-items: center;
  }

  .ineligible {
    font-size: 0.8rem;
    color: var(--muted);
  }

  .decision-form {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding-top: 0.5rem;
    border-top: 1px solid var(--border);
  }

  .decision-summary {
    margin: 0;
    font-size: 0.9rem;
    color: var(--fg);
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

  .btn.danger {
    background: transparent;
    color: #c0392b;
    border: 1px solid #c0392b;
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
  }


  .error {
    color: #b91c1c;
  }
</style>
