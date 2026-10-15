<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Username from '$lib/components/Username.svelte';
  import { formatRelativeTime, formatAbsoluteDateTime, fromUnix } from '$lib/utils/time';
  import { chainOrder, ledgerRepository, type LedgerRecord } from '$lib/repositories/ledger';
  import { ledgerChanged, ledgerTampered, syncLedger } from '$lib/services/ledger';
  import { parsePermissionSet } from '$lib/utils/permissions';

  let entries: LedgerRecord[] = [];
  let loaded = false;

  async function load(): Promise<void> {
    entries = chainOrder(await ledgerRepository.getAll()).reverse();
    loaded = true;
  }

  $: $ledgerChanged, void load();

  onMount(() => {
    void syncLedger();
  });
</script>

<Auth>
  <SideNav />
  <div class="ledger-container">
    <div class="ledger-content">
      <h1 class="title">Ledger</h1>

      {#if $ledgerTampered}
        <p class="warning" role="alert">
          The server’s ledger no longer matches the copy on this device. This device keeps its own copy.
        </p>
      {/if}

      {#if loaded && entries.length === 0}
        <p class="empty">No admin actions yet.</p>
      {:else}
        <ol class="entry-list">
          {#each entries as e (e.id)}
            {@const signedAt = fromUnix(e.serverSignature.signedAt) ?? new Date(0)}
            {@const requestedAt = e.requestedAt ? fromUnix(e.requestedAt) : null}
            {@const permissions = e.content ? parsePermissionSet(e.content) : null}
            {@const verbs = e.kind === 'admin-invite' ? ['Created', 'Claimed'] : ['Proposed', 'Approved']}
            <li class="entry" class:server-key={e.kind === 'server-key'}>
              <span class="entry-id">#{e.id}</span>
              {#if e.kind === 'server-key'}
                <span class="badge">Server key rotated</span>
              {/if}
              <p class="message">{e.message}</p>
              {#if e.targets.length > 0}
                <p class="targets">
                  {#each e.targets as t, i (t)}{#if i > 0}, {/if}<Username userID={t} color="var(--primary)" />{/each}
                </p>
              {/if}
              {#if permissions}
                <ul class="permissions">
                  {#each permissions as p (p)}<li>{p}</li>{/each}
                </ul>
              {:else if e.content}
                <blockquote class="content">{e.content}</blockquote>
              {/if}
              {#if e.requesterID && requestedAt}
                <p class="meta">
                  {verbs[0]} by <Username userID={e.requesterID} color="var(--primary)" />
                  <time datetime={requestedAt.toISOString()} title={formatAbsoluteDateTime(requestedAt)}>
                    {formatRelativeTime(requestedAt)}
                  </time>
                </p>
                <p class="meta">
                  {verbs[1]} by <Username userID={e.userID} color="var(--primary)" />
                  <time datetime={signedAt.toISOString()} title={formatAbsoluteDateTime(signedAt)}>
                    {formatRelativeTime(signedAt)}
                  </time>
                </p>
              {:else}
                <div class="meta">
                  {#if e.userID}
                    <Username userID={e.userID} color="var(--primary)" />
                  {:else}
                    <span>Server operator</span>
                  {/if}
                  <time datetime={signedAt.toISOString()} title={formatAbsoluteDateTime(signedAt)}>
                    {formatRelativeTime(signedAt)}
                  </time>
                </div>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
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

  .title {
    margin: 0 0 1rem;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .warning {
    margin: 0 0 1rem;
    padding: 0.75rem 1rem;
    border: 1px solid var(--error);
    border-radius: 8px;
    color: var(--error);
    font-size: 0.9rem;
  }

  .empty {
    color: var(--muted);
    font-size: 0.9rem;
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

  .entry.server-key {
    border-left: 3px solid var(--error);
  }

  .badge {
    align-self: flex-start;
    padding: 0 0.5rem;
    border: 1px solid var(--error);
    border-radius: 999px;
    color: var(--error);
    font-size: 0.75rem;
    font-weight: 600;
  }

  .message,
  .targets {
    margin: 0;
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .targets :global(.username) {
    font-weight: 600;
  }

  .content {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
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

  .meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem;
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .entry-id {
    color: var(--muted);
    font-family: monospace;
    font-size: 0.8rem;
  }
</style>
