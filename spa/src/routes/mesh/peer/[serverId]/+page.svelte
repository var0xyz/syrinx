<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import type * as api from '$lib/types/api';
  import { notificationStore } from '$lib/stores/notifications';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Username from '$lib/components/Username.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  $: serverId = $page.params.serverId;

  let user: api.User | null = null;
  let isAdmin = false;
  let loading = true;
  let server: api.FederationServer | null = null;
  /** null when this server was the responder (no local invitation row). */
  let invitation: api.FederationInvitation | null = null;
  /** Plain text, invite logs then server logs — rendered verbatim, never parsed. */
  let logsText = '';

  onMount(async () => {
    user = await authService.getCurrentUser();
    if (!user) return;
    isAdmin = user.role === 'admin' || user.role === 'root';
    if (!isAdmin) {
      loading = false;
      return;
    }
    await refresh();
    loading = false;
  });

  async function refresh() {
    try {
      const [servers, inv, serverLogs] = await Promise.all([
        apiService.listFederationServers(),
        apiService.getFederationServerInvitation(serverId),
        apiService.getFederationServerLogs(serverId),
      ]);
      server = servers.find((s) => s.serverId === serverId) ?? null;
      invitation = inv;
      logsText = serverLogs;
    } catch (err) {
      console.error('[mesh/peer]', err);
      notificationStore.error(err instanceof Error ? err.message : 'Failed to load server logs');
    }
  }

  function reviewActionLabel(status: api.FederationInvitation['status']) {
    return `${status.charAt(0).toUpperCase()}${status.slice(1)} by`;
  }

  /** Prefer history.back so the mesh list can restore scroll. */
  function goBack() {
    if (typeof history !== 'undefined' && history.length > 1) {
      history.back();
      return;
    }
    goto('/mesh');
  }
</script>

<Auth>
  <div class="mesh-container">
    <div class="mesh-content">
      {#if loading}
        <p class="muted">Loading…</p>
      {:else if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        <button class="back-link" on:click={goBack}>&larr; Back to mesh</button>

        {#if server}
          <div class="server-header">
            <span class="server-name">{server.name}</span>
            <span class="badge" data-status={server.connected ? 'approved' : 'accepted'}>
              {server.connected ? 'Connected' : 'Awaiting confirmation'}
            </span>
          </div>
          {#if server.baseUrl}
            <p class="meta">{server.baseUrl}</p>
          {/if}
          <p class="meta">Added {formatRelativeTime(server.createdAt)}</p>
        {:else}
          <p class="muted">Unknown server: {serverId}</p>
        {/if}

        {#if invitation}
          <h3 class="section-heading">Invitation</h3>
          <div class="invite-info">
            <span class="meta"
              >"{invitation.name}" created {formatRelativeTime(invitation.createdAt)} by <Username
                userID={invitation.createdBy}
                username={invitation.createdByUsername}
                class="meta-link"
                at
                fire={false}
              /></span
            >
            {#if invitation.reviewedBy && invitation.reviewedByUsername}
              <span class="meta">
                {reviewActionLabel(invitation.status)}
                <Username
                  userID={invitation.reviewedBy}
                  username={invitation.reviewedByUsername}
                  class="meta-link"
                  at
                  fire={false}
                />
                {#if invitation.reviewedAt}
                  · {formatRelativeTime(invitation.reviewedAt)}
                {/if}
              </span>
            {/if}
          </div>
        {/if}

        <h3 class="section-heading">Logs</h3>
        {#if !logsText}
          <div class="empty-state">
            <div class="empty-icon">📜</div>
            <h3>No log lines yet</h3>
            <p>Handshake and connection events for this server will show up here.</p>
          </div>
        {:else}
          <pre class="log-text">{logsText}</pre>
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="mesh" />
  </div>
</Auth>

<style>
  .mesh-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .mesh-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .back-link {
    background: none;
    border: none;
    color: var(--primary);
    font-size: 0.9rem;
    cursor: pointer;
    padding: 0;
    margin-bottom: 1rem;
  }

  .server-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.25rem;
  }

  .server-name {
    font-weight: 600;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .badge {
    font-size: 0.75rem;
    font-weight: 600;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: var(--input-bg);
  }

  .badge[data-status='accepted'] {
    color: #d68910;
  }

  .badge[data-status='approved'] {
    color: #27ae60;
  }

  .meta {
    font-size: 0.85rem;
    color: var(--muted);
    margin: 0.15rem 0;
  }

  .invite-info {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.75rem 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  :global(.invite-info .meta-link) {
    color: var(--primary);
    text-decoration: none;
  }

  .muted {
    color: var(--muted);
  }

  .error {
    color: #b91c1c;
  }

  .section-heading {
    margin: 1.5rem 0 0.75rem 0;
    font-size: 0.95rem;
    color: var(--fg);
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

  .empty-state p {
    margin: 0;
    line-height: 1.4;
  }

  .log-text {
    margin: 0;
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--fg);
    font-family: ui-monospace, monospace;
    font-size: 0.8rem;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    overflow-x: auto;
  }

  @media (max-width: 768px) {
    .mesh-content {
      padding: 0.5rem;
    }
  }
</style>
