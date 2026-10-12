<script lang="ts">
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import AdminActionsModal from '$lib/components/AdminActionsModal.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  // Mockup: sample data only, nothing here talks to the server yet.
  type Action = 'suspend' | 'reinstate' | 'kick' | 'promote' | 'demote' | 'rebind' | 'broadcast' | 'message';
  type State = 'pending' | 'executed' | 'rejected' | 'withdrawn' | 'moot';

  interface MockRequest {
    id: string;
    action: Action;
    requester: string;
    targets: string[];
    droppedTargets?: string[];
    reason: string;
    createdAt: Date;
    state: State;
    closedBy?: string;
    closedAt?: Date;
    // Why the viewer can't approve, mirroring the server's eligibility rules.
    ineligible?: string;
    own?: boolean;
    fingerprint?: string;
  }

  interface MockReport {
    reporter: string;
    reporterServer?: string;
    note?: string;
    at: Date;
  }

  interface MockFlag {
    reedID: string;
    author: string;
    content: string | null;
    reports: MockReport[];
  }

  const tabs = [
    { href: '/admin/actions', label: 'Actions' },
    { href: '/admin/broadcast', label: 'Broadcast' },
    { href: '/admin/mesh', label: 'Mesh' },
  ];

  const modeOptions = [
    { value: 'requests', label: 'Requests' },
    { value: 'flagged', label: 'Flagged' },
  ];

  let mode: 'requests' | 'flagged' = 'requests';

  const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000);

  let requests: MockRequest[] = [
    {
      id: 'r1',
      action: 'kick',
      requester: 'marta',
      targets: ['spamlord', 'spamlord_2', 'spamlord_3'],
      reason: 'Three accounts from the same invite posting scam links in every pipe since Tuesday.',
      createdAt: ago(42),
      state: 'pending',
    },
    {
      id: 'r2',
      action: 'rebind',
      requester: 'jonas',
      targets: ['oldfriend'],
      reason: 'Lost their phone on a trip, no backup. Verified in person at the meetup, read back the fingerprint.',
      fingerprint: '7F3A 91C2 0B44 E1D8 5A7C  3E90 12BF 64D1 A8C3 2F06',
      createdAt: ago(60 * 5),
      state: 'pending',
    },
    {
      id: 'r3',
      action: 'demote',
      requester: 'marta',
      targets: ['ines'],
      reason: 'Ines asked to step down as admin while she is away for a few months.',
      createdAt: ago(60 * 26),
      state: 'pending',
      ineligible: 'Needs root: the target is an admin',
    },
    {
      id: 'r4',
      action: 'broadcast',
      requester: 'you',
      targets: [],
      reason: 'The server will be down for maintenance on Saturday from 02:00 to 04:00 UTC.',
      createdAt: ago(60 * 3),
      state: 'pending',
      own: true,
      ineligible: 'You opened this request',
    },
    {
      id: 'r9',
      action: 'message',
      requester: 'jonas',
      targets: ['hotheaded'],
      reason: 'Hi, a few members raised your replies in the newcomers pipe. Can we talk this week before anything else happens?',
      createdAt: ago(60 * 2),
      state: 'pending',
    },
    {
      id: 'r5',
      action: 'suspend',
      requester: 'pablo',
      targets: ['hotheaded'],
      droppedTargets: ['leftalready'],
      reason: 'Repeated harassment in replies after two warnings. Suspending while we talk to them.',
      createdAt: ago(60 * 30),
      state: 'pending',
      ineligible: "You're in pablo's invite tree",
    },
    {
      id: 'r6',
      action: 'promote',
      requester: 'jonas',
      targets: ['lucia'],
      reason: 'Lucia has been helping new members for months and agreed to take on admin duties.',
      createdAt: ago(60 * 24 * 3),
      state: 'executed',
      closedBy: 'marta',
      closedAt: ago(60 * 24 * 2),
    },
    {
      id: 'r7',
      action: 'kick',
      requester: 'pablo',
      targets: ['grumpy'],
      reason: 'Rude in a single thread.',
      createdAt: ago(60 * 24 * 6),
      state: 'rejected',
      closedBy: 'jonas',
      closedAt: ago(60 * 24 * 5),
    },
    {
      id: 'r8',
      action: 'reinstate',
      requester: 'marta',
      targets: ['nightowl'],
      reason: 'Agreed to the rules after the suspension call.',
      createdAt: ago(60 * 24 * 9),
      state: 'moot',
      closedAt: ago(60 * 24 * 8),
    },
  ];

  let flags: MockFlag[] = [
    {
      reedID: 'f1',
      author: 'spamlord',
      content: 'Double your coins in 24h!! DM me for the link, only 10 spots left 🚀🚀',
      reports: [
        { reporter: 'lucia', note: 'Same message in three pipes', at: ago(30) },
        { reporter: 'oldfriend', at: ago(55) },
        { reporter: 'nightowl', note: 'Scam', at: ago(90) },
      ],
    },
    {
      reedID: 'f2',
      author: 'hotheaded',
      content: 'Nobody asked for your opinion, go back to whatever hole you crawled out of.',
      reports: [
        { reporter: 'ines', note: 'Reply to a new member', at: ago(60 * 4) },
        { reporter: 'kim', reporterServer: 'mom.example.org', note: 'Said this to me too', at: ago(60 * 5) },
      ],
    },
    {
      reedID: 'f3',
      author: 'wanderer',
      content: null,
      reports: [{ reporter: 'theo', reporterServer: 'mom.example.org', at: ago(60 * 20) }],
    },
  ];

  let actingOn: string | null = null;

  function dismiss(reedID: string) {
    flags = flags.filter((f) => f.reedID !== reedID);
  }

  const actionLabel: Record<Action, string> = {
    suspend: 'Suspend',
    reinstate: 'Reinstate',
    kick: 'Kick',
    promote: 'Promote',
    demote: 'Demote',
    rebind: 'Key rebind',
    broadcast: 'Broadcast',
    message: 'Message',
  };

  const stateLabel: Record<State, string> = {
    pending: 'Pending',
    executed: 'Executed',
    rejected: 'Rejected',
    withdrawn: 'Withdrawn',
    moot: 'Moot',
  };

  function summary(r: MockRequest): string {
    const n = r.targets.length;
    const accounts = n === 1 ? '1 account' : `${n} accounts`;
    switch (r.action) {
      case 'kick':
        return `Removes ${accounts} and all their content. This can't be undone.`;
      case 'suspend':
        return `Blocks ${accounts} from signing in until reinstated. Peers see no change.`;
      case 'reinstate':
        return `Lifts the suspension on ${accounts}.`;
      case 'promote':
        return 'Makes this user an admin.';
      case 'demote':
        return 'Makes this admin a regular user and revokes their unclaimed admin invites.';
      case 'rebind':
        return 'Revokes their current key as lost and binds the uploaded key, restricted to a rotation.';
      case 'broadcast':
        return 'Sends this message to every local member’s mailbox.';
      case 'message':
        return 'Sends this message privately to their mailbox. It stays out of the ledger.';
    }
  }

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';

  $: pending = requests.filter((r) => r.state === 'pending');
  $: closed = requests
    .filter((r) => r.state !== 'pending')
    .sort((a, b) => (b.closedAt?.getTime() ?? 0) - (a.closedAt?.getTime() ?? 0));

  // One open confirmation at a time.
  let deciding: { id: string; decision: 'approve' | 'reject' | 'withdraw' } | null = null;

  function openDecision(id: string, decision: 'approve' | 'reject' | 'withdraw') {
    deciding = { id, decision };
  }

  function submitDecision() {
    if (!deciding) return;
    const { id, decision } = deciding;
    const state: State = decision === 'approve' ? 'executed' : decision === 'reject' ? 'rejected' : 'withdrawn';
    requests = requests.map((r) => (r.id === id ? { ...r, state, closedBy: 'you', closedAt: new Date() } : r));
    deciding = null;
  }

  const decisionLabel = { approve: 'Approve', reject: 'Reject', withdraw: 'Withdraw' };
</script>

{#snippet requestCard(r: MockRequest)}
  <li class="request" data-action={r.action}>
    <div class="request-head">
      <span class="action-badge" data-action={r.action}>{actionLabel[r.action]}</span>
      {#if r.state !== 'pending'}
        <span class="state-badge" data-state={r.state}>{stateLabel[r.state]}</span>
      {/if}
      <span class="meta when">{formatRelativeTime(r.closedAt ?? r.createdAt)}</span>
    </div>

    {#if r.targets.length > 0}
      <p class="targets">
        {#each r.targets as t, i (t)}{#if i > 0}, {/if}<span class="name">{t}</span>{/each}{#each r.droppedTargets ?? [] as t (t)}, <span class="name dropped" title="Left before approval">{t}</span>{/each}
      </p>
    {:else}
      <p class="targets everyone">Every local member</p>
    {/if}

    <blockquote class="reason">{r.reason}</blockquote>

    {#if r.fingerprint}
      <div class="fingerprint">
        <span class="meta">New key fingerprint</span>
        <code>{r.fingerprint}</code>
      </div>
    {/if}

    <p class="meta">
      Requested by <span class="name">{r.requester}</span>
      {#if r.state === 'executed' || r.state === 'rejected'}
        · {r.state === 'executed' ? 'approved' : 'rejected'} by <span class="name">{r.closedBy}</span>
      {:else if r.state === 'moot'}
        · closed by the server: every target left
      {/if}
    </p>

    {#if r.state === 'pending'}
      {#if deciding?.id === r.id}
        <div class="decision-form">
          {#if deciding.decision === 'approve'}
            <p class="decision-summary">{summary(r)}</p>
          {/if}
          <div class="decision-actions">
            <button class="btn secondary" type="button" on:click={() => (deciding = null)}>Cancel</button>
            <button
              class="btn"
              class:primary={deciding.decision === 'approve'}
              class:danger={deciding.decision !== 'approve'}
              type="button"
              on:click={submitDecision}
            >
              Sign and {decisionLabel[deciding.decision].toLowerCase()}
            </button>
          </div>
        </div>
      {:else}
        <div class="request-actions">
          {#if r.own}
            <button class="btn secondary" type="button" on:click={() => openDecision(r.id, 'withdraw')}>
              Withdraw
            </button>
          {:else}
            <button
              class="btn secondary"
              type="button"
              disabled={!!r.ineligible}
              on:click={() => openDecision(r.id, 'reject')}
            >
              Reject
            </button>
            <button
              class="btn primary"
              type="button"
              disabled={!!r.ineligible}
              on:click={() => openDecision(r.id, 'approve')}
            >
              Approve
            </button>
          {/if}
          {#if r.ineligible}
            <span class="ineligible">{r.ineligible}</span>
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
        <p class="mock-banner">Mockup: sample data, nothing is sent to the server.</p>

        <div class="mode-picker">
          <ModeSwitch options={modeOptions} bind:value={mode} />
        </div>

        {#if mode === 'requests'}
          {#if pending.length === 0}
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
              {#each closed as r (r.id)}
                {@render requestCard(r)}
              {/each}
            </ul>
          {/if}
        {:else}
          {#if flags.length === 0}
            <div class="empty-state">
              <div class="empty-icon">🚩</div>
              <h3>No open flags</h3>
              </div>
          {:else}
            <ul class="flag-list">
              {#each flags as f (f.reedID)}
                <li class="flag">
                  <div class="flag-head">
                    <span class="author name">{f.author}</span>
                    <span class="report-count">
                      {f.reports.length} {f.reports.length === 1 ? 'flag' : 'flags'}
                    </span>
                  </div>

                  {#if f.content}
                    <blockquote class="content">{f.content}</blockquote>
                  {:else}
                    <p class="content loading">Retrieving from the network…</p>
                  {/if}

                  <ul class="reports">
                    {#each f.reports as rep (`${rep.reporter}@${rep.reporterServer ?? ''}`)}
                      <li>
                        <span class="name">{rep.reporter}</span>{#if rep.reporterServer}<span class="server">({rep.reporterServer})</span>{/if}
                        {#if rep.note}<span class="note">“{rep.note}”</span>{/if}
                        <span class="report-when">{formatRelativeTime(rep.at)}</span>
                      </li>
                    {/each}
                  </ul>

                  <div class="flag-actions">
                    <button class="btn secondary" type="button" disabled>Open reed</button>
                    <button class="btn secondary" type="button" on:click={() => (actingOn = f.author)}>
                      Act on <span class="name">{f.author}</span>
                    </button>
                    <button class="btn secondary dismiss" type="button" on:click={() => dismiss(f.reedID)}>
                      Dismiss
                    </button>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>

  {#if actingOn}
    <AdminActionsModal
      open
      userID={actingOn}
      username={actingOn}
      viewerIsRoot={$page.data?.user?.role === 'root'}
      on:close={() => (actingOn = null)}
    />
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

  .mock-banner {
    margin: 0 0 1rem;
    padding: 0.5rem 0.75rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.8rem;
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

  .when {
    margin-left: auto;
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
