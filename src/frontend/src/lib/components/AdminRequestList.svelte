<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import Username from '$lib/components/Username.svelte';
  import { apiService } from '$lib/services/api';
  import { cryptoService } from '$lib/services/crypto';
  import {
    ACTION_PERMISSION,
    adminRequestsChanged,
    adminRecordsChanged,
    decideAdminRequest,
    decisionBlocker,
    syncAdminRecords,
  } from '$lib/services/admin';
  import { adminRequestsRepository } from '$lib/repositories/adminRequests';
  import { parsePermissionSet } from '$lib/utils/permissions';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { adminPermissions, holds } from '$lib/stores/adminPermissions';
  import type * as api from '$lib/types/api';

  type Decision = 'approve' | 'reject' | 'withdraw';

  /** Which requests this list shows. */
  export let include: (r: api.AdminRequest) => boolean;
  /** Whether to list the open requests or the closed ones. */
  export let state: 'open' | 'closed';
  export let emptyTitle: string;

  const actionLabel: Record<string, string> = {
    suspend: 'Suspend',
    reinstate: 'Reinstate',
    kick: 'Kick',
    promote: 'Promote',
    demote: 'Demote',
    permissions: 'Permissions',
    rebind: 'Key rebind',
    invites: 'Invites',
    broadcast: 'Broadcast',
    unicast: 'Unicast',
  };

  const stateLabel: Record<string, string> = {
    pending: 'Pending',
    executed: 'Executed',
    rejected: 'Rejected',
    withdrawn: 'Withdrawn',
    moot: 'Moot',
  };

  const decisionLabel: Record<Decision, string> = { approve: 'Approve', reject: 'Reject', withdraw: 'Withdraw' };

  let pending: api.AdminRequest[] = [];
  let pendingLoaded = false;
  let pendingError = '';
  let closed: api.AdminRequest[] = [];
  let closedLoaded = false;
  const CLOSED_PAGE = 50;
  let closedShown = CLOSED_PAGE;
  let fingerprints: Record<string, string> = {};

  // One open confirmation at a time.
  let deciding: { id: string; decision: Decision } | null = null;
  let decisionError = '';
  let submitting = false;

  $: viewerID = $page.data?.user?.id ?? '';
  // The permissions read at app load; the server checks every decision.
  $: viewer = {
    role: $page.data?.user?.role ?? '',
    permissions: Object.values(ACTION_PERMISSION).filter((p) => holds($page.data?.user, $adminPermissions, p)),
  };

  onMount(() => void loadClosed());

  $: $adminRequestsChanged, void loadRequests();
  $: $adminRecordsChanged, void loadClosed();

  // Open requests are always read from the server: deciding needs it anyway.
  async function loadRequests(): Promise<void> {
    let all: api.AdminRequest[];
    try {
      all = (await apiService.listAdminRequests('pending')).requests;
      pending = all.filter(include);
      pendingError = '';
    } catch (err) {
      pendingError = err instanceof Error ? err.message : 'Could not load open requests.';
      return;
    } finally {
      pendingLoaded = true;
    }
    fingerprintsFor(pending);
    await syncAdminRecords();
  }

  // Closed requests never change, so they come from the local copy.
  async function loadClosed(): Promise<void> {
    closed = (await adminRequestsRepository.getAll()).filter((r) => r.state !== 'pending' && include(r));
    closedLoaded = true;
    fingerprintsFor(closed);
  }

  function fingerprintsFor(reqs: api.AdminRequest[]): void {
    for (const r of reqs) {
      if (r.action === 'rebind' && !fingerprints[r.id]) {
        cryptoService.fingerprintOf(r.body).then((fp) => (fingerprints = { ...fingerprints, [r.id]: grouped(fp) })).catch(() => {});
      }
    }
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
      case 'invites':
        return `Lets them create ${r.body} more invite${r.body === '1' ? '' : 's'} than the server allows.`;
      case 'broadcast':
        return 'Sends this message to every local member’s mailbox.';
      case 'unicast':
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
      const message = err instanceof Error ? err.message : '';
      decisionError =
        message === 'opened-before-recovery'
          ? 'This request was opened before the server’s recovery. It can be decided once recovery is over.'
          : message || 'The decision was refused.';
    } finally {
      submitting = false;
    }
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
      {#if r.action === 'unicast'}<span class="state-badge">Private</span>{/if}
    </div>

    {#if r.targets.length > 0}
      <p class="targets">
        {#each remaining(r) as t, i (t)}{#if i > 0}, {/if}<span class="name"><Username userID={t} color="var(--primary)" /></span>{/each}{#each r.droppedTargets as t (t)}, <span class="name dropped" title="Left before approval"><Username userID={t} linked={false} color="var(--muted)" /></span>{/each}
      </p>
    {:else}
      <p class="targets everyone">Every local member</p>
    {/if}

    {#if r.action === 'broadcast' || r.action === 'unicast'}
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
              {decisionLabel[deciding.decision]}
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

<div class="admin-requests">
  {#if state === 'open'}
    {#if pendingError}
      <p class="error" role="alert">{pendingError}</p>
    {:else if !pendingLoaded}
      <!-- Open requests come from the server; nothing to show until then. -->
    {:else if pending.length === 0}
      <div class="empty-state">
        <h3>{emptyTitle}</h3>
      </div>
    {:else}
      <ul class="request-list">
        {#each pending as r (r.id)}
          {@render requestCard(r)}
        {/each}
      </ul>
    {/if}
  {:else if closedLoaded && closed.length === 0}
    <div class="empty-state">
      <h3>{emptyTitle}</h3>
    </div>
  {:else}
    <ul class="request-list">
      {#each closed.slice(0, closedShown) as r (r.id)}
        {@render requestCard(r)}
      {/each}
    </ul>
    {#if closed.length > closedShown}
      <button class="btn secondary more" type="button" on:click={() => (closedShown += CLOSED_PAGE)}>Load more</button>
    {/if}
  {/if}
</div>

<style>
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
    overflow-wrap: anywhere;
  }

  .request[data-action='kick'] {
    border-left: 3px solid #c0392b;
  }

  .request-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  .action-badge,
  .state-badge {
    white-space: nowrap;
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
  .action-badge[data-action='reinstate'],
  .action-badge[data-action='invites'] {
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

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .error {
    color: #b91c1c;
  }
</style>
