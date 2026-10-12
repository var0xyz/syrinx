<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import { formatRelativeTime, formatAbsoluteDateTime } from '$lib/utils/time';

  // Mockup: sample entries only, nothing is read from the local store or the server.
  interface MockEntry {
    id: number;
    userID?: string;
    message: (string | { name: string })[];
    signedAt: Date;
  }

  const n = (name: string) => ({ name });

  const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000);

  const entries: MockEntry[] = [
    { id: 18, userID: 'pablo', message: ['Approved request e41b07'], signedAt: ago(10) },
    {
      id: 17,
      userID: 'marta',
      message: ['Request e41b07: broadcast "The server will be down for maintenance on Saturday from 02:00 to 04:00 UTC."'],
      signedAt: ago(60 * 3),
    },
    { id: 16, userID: 'jonas', message: ['Approved request a3f9c2'], signedAt: ago(45) },
    { id: 15, userID: 'marta', message: ['Approved request 5d20e8'], signedAt: ago(60 * 4) },
    {
      id: 14,
      userID: 'jonas',
      message: ['Request 5d20e8: add key 7F3A91C20B44E1D8 to ', n('oldfriend'), ' for: Lost their phone, verified in person'],
      signedAt: ago(60 * 5),
    },
    {
      id: 13,
      userID: 'marta',
      message: ['Request a3f9c2: kick ', n('spamlord'), ', ', n('spam lord 2'), ' for: Scam links in every pipe since Tuesday'],
      signedAt: ago(60 * 6),
    },
    { id: 12, userID: 'ines', message: ['Approved connection to mom.example.org'], signedAt: ago(60 * 26) },
    { id: 11, userID: 'pablo', message: ['Server invite 0c7d51 created for Mom’s server'], signedAt: ago(60 * 27) },
    { id: 10, userID: 'marta', message: ['Approved request 9b6e14'], signedAt: ago(60 * 24 * 2) },
    {
      id: 9,
      userID: 'jonas',
      message: ['Request 9b6e14: promote ', n('lucia'), ' to admin for: Has been helping new members for months'],
      signedAt: ago(60 * 24 * 3),
    },
    { id: 8, userID: 'jonas', message: ['Rejected request 27fa90'], signedAt: ago(60 * 24 * 5) },
    { id: 7, userID: 'pablo', message: ['Request 27fa90: kick ', n('grumpy'), ' for: Rude in a single thread'], signedAt: ago(60 * 24 * 6) },
    { id: 6, message: ['Closed request c18d33: every target left'], signedAt: ago(60 * 24 * 8) },
    {
      id: 5,
      userID: 'marta',
      message: ['Request c18d33: reinstate ', n('nightowl'), ' for: Agreed to the rules after the suspension call'],
      signedAt: ago(60 * 24 * 9),
    },
    { id: 4, userID: 'root', message: ['Username ', n('admin'), ' reserved'], signedAt: ago(60 * 24 * 12) },
    { id: 3, userID: 'ines', message: ['Disconnected from noisy.example.net'], signedAt: ago(60 * 24 * 15) },
  ];
</script>

<Auth>
  <SideNav />
  <div class="ledger-container">
    <div class="ledger-content">
      <p class="mock-banner">Mockup: sample data, nothing is read from the server.</p>

      <h1 class="title">Ledger</h1>

      <ol class="entry-list">
        {#each entries as e (e.id)}
          <li class="entry">
            <p class="message">{#each e.message as part}{#if typeof part === 'string'}{part}{:else}<span class="name">{part.name}</span>{/if}{/each}</p>
            <div class="meta">
              <span class="entry-id">#{e.id}</span>
              {#if e.userID}<span class="name">{e.userID}</span>{:else}<span>Server</span>{/if}
              <time datetime={e.signedAt.toISOString()} title={formatAbsoluteDateTime(e.signedAt)}>
                {formatRelativeTime(e.signedAt)}
              </time>
            </div>
          </li>
        {/each}
      </ol>
    </div>

    <BottomToolbar currentPage="account" />
  </div>
</Auth>

<style>
  .ledger-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .ledger-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .ledger-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .ledger-content {
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

  .title {
    margin: 0 0 1rem;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .entry-list {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    overflow: hidden;
  }

  .entry {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    padding: 0.75rem 1rem;
  }

  .entry + .entry {
    border-top: 1px solid var(--border);
  }

  .message {
    margin: 0;
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .name {
    color: var(--primary);
    font-weight: 600;
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .entry-id {
    font-family: monospace;
  }
</style>
