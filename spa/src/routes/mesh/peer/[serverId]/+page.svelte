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
  let approving = false;
  let rejecting = false;
  let showRejectModal = false;
  let rejectReason = '';

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

  /** Prefer history.back so the mesh list can restore scroll. */
  function goBack() {
    if (typeof history !== 'undefined' && history.length > 1) {
      history.back();
      return;
    }
    goto('/mesh');
  }

  async function approve() {
    if (approving) return;
    approving = true;
    try {
      await apiService.approveFederationServer(serverId);
      notificationStore.success('Server approved');
      await refresh();
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to approve server');
    } finally {
      approving = false;
    }
  }

  function openRejectModal() {
    if (rejecting) return;
    rejectReason = '';
    showRejectModal = true;
  }

  function dismissRejectModal() {
    if (rejecting) return;
    showRejectModal = false;
    rejectReason = '';
  }

  async function reject() {
    const reason = rejectReason.trim();
    if (!reason || rejecting) return;
    rejecting = true;
    try {
      await apiService.rejectFederationServer(serverId, reason);
      showRejectModal = false;
      notificationStore.success('Server rejected');
      goto('/mesh');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to reject server');
    } finally {
      rejecting = false;
    }
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
            <span class="server-name">{server.name} ({serverId})</span>
            <span class="badge" data-status={server.connected ? 'approved' : 'accepted'}>
              {server.connected ? 'Connected' : 'Awaiting confirmation'}
            </span>
          </div>
          {#if server.baseUrl}
            <p class="meta">{server.baseUrl}</p>
          {/if}
          <p class="meta">Added {formatRelativeTime(server.createdAt)}</p>
          {#if invitation}
            <p class="meta"
              >Invited by <Username
                userID={invitation.createdBy}
                username={invitation.createdByUsername}
                class="meta-link"
                at
                fire={false}
              /> {formatRelativeTime(invitation.createdAt)}</p
            >
          {/if}

          {#if !server.connected}
            <div class="approval-actions">
              <button class="btn danger" disabled={approving || rejecting} on:click={openRejectModal}>
                Reject
              </button>
              <button class="btn primary" disabled={approving || rejecting} on:click={approve}>
                {approving ? 'Approving…' : 'Approve'}
              </button>
            </div>
          {/if}
        {:else}
          <p class="muted">Unknown server: {serverId}</p>
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

  {#if showRejectModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="reject-server-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissRejectModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissRejectModal()}
    >
      <div class="modal">
        <h2 id="reject-server-title">Reject server</h2>
        <p class="modal-lead">Explain why this connection is being rejected. This is recorded in the server's log.</p>
        <label class="field">
          <span>Reason</span>
          <textarea
            bind:value={rejectReason}
            rows="4"
            spellcheck="true"
            placeholder="e.g. Unexpected connection request"
          ></textarea>
        </label>
        <div class="modal-actions">
          <button class="btn secondary" disabled={rejecting} on:click={dismissRejectModal}>
            Cancel
          </button>
          <button class="btn danger" disabled={rejecting || !rejectReason.trim()} on:click={reject}>
            {rejecting ? 'Rejecting…' : 'Reject'}
          </button>
        </div>
      </div>
    </div>
  {/if}
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

  :global(.meta .meta-link) {
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

  .approval-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.75rem;
  }

  .btn {
    border: none;
    border-radius: 8px;
    padding: 0.6rem 1rem;
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

  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
    padding: 1rem;
  }

  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.25rem;
    max-width: 480px;
    width: 100%;
  }

  .modal h2 {
    margin: 0 0 0.75rem 0;
    font-size: 1.2rem;
  }

  .modal-lead {
    margin: 0 0 1rem 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.4;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    margin-bottom: 1rem;
  }

  .field textarea {
    font-family: inherit;
    font-size: 0.9rem;
    padding: 0.5rem;
    border-radius: 0.35rem;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    resize: vertical;
  }

  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  .modal-actions .btn {
    width: auto;
    min-width: 5.5rem;
  }

  @media (max-width: 768px) {
    .mesh-content {
      padding: 0.5rem;
    }
  }
</style>
