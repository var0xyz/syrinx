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
  let isRoot = false;
  let loading = true;
  let server: api.FederationServer | null = null;
  /** null when this server was the responder (no local invitation row). */
  let invitation: api.FederationInvitation | null = null;
  /** The (approved) attempt that produced this server — approved-by/dates. */
  let attempt: api.FederationAttempt | null = null;
  /** Plain text, attempt logs then server logs — rendered verbatim, never parsed. */
  let logsText = '';

  let requesting = false;
  let showRevokeModal = false;
  let revokeReason = '';

  let confirming = false;
  let cancelling = false;

  let purging = false;
  let showPurgeModal = false;
  let purgeConfirmText = '';

  // Root may confirm its own disconnect request (single-operator servers
  // have no second admin to ask) — every other admin must have someone
  // else confirm. Mirrors the server-side check in
  // ConfirmFederationServerDisconnect.
  $: isOwnDisconnectRequest = !!(server?.disconnectPending && server.disconnectRequestedBy === user?.id);
  $: confirmBlocked = isOwnDisconnectRequest && !isRoot;

  onMount(async () => {
    user = await authService.getCurrentUser();
    if (!user) return;
    isAdmin = user.role === 'admin' || user.role === 'root';
    isRoot = user.role === 'root';
    if (!isAdmin) {
      loading = false;
      return;
    }
    await refresh();
    loading = false;
  });

  async function refresh() {
    try {
      const [servers, inv, att, serverLogs] = await Promise.all([
        apiService.listFederationServers(),
        apiService.getFederationServerInvitation(serverId),
        apiService.getFederationServerAttempt(serverId),
        apiService.getFederationServerLogs(serverId),
      ]);
      server = servers.find((s) => s.serverId === serverId) ?? null;
      invitation = inv;
      attempt = att;
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

  function openRevokeModal() {
    if (requesting) return;
    revokeReason = '';
    showRevokeModal = true;
  }

  function dismissRevokeModal() {
    if (requesting) return;
    showRevokeModal = false;
    revokeReason = '';
  }

  async function requestDisconnect() {
    const reason = revokeReason.trim();
    if (!reason || requesting) return;
    requesting = true;
    try {
      await apiService.requestFederationServerDisconnect(serverId, reason);
      showRevokeModal = false;
      notificationStore.success('Disconnect requested — a different admin must confirm it');
      await refresh();
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to request disconnect');
    } finally {
      requesting = false;
    }
  }

  async function confirmDisconnect() {
    if (confirming) return;
    confirming = true;
    try {
      await apiService.confirmFederationServerDisconnect(serverId);
      notificationStore.success('Server disconnected');
      await refresh();
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to confirm disconnect');
    } finally {
      confirming = false;
    }
  }

  async function cancelDisconnect() {
    if (cancelling) return;
    cancelling = true;
    try {
      await apiService.cancelFederationServerDisconnect(serverId);
      notificationStore.success('Disconnect request cancelled');
      await refresh();
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to cancel disconnect request');
    } finally {
      cancelling = false;
    }
  }

  function openPurgeModal() {
    if (purging) return;
    purgeConfirmText = '';
    showPurgeModal = true;
  }

  function dismissPurgeModal() {
    if (purging) return;
    showPurgeModal = false;
    purgeConfirmText = '';
  }

  async function purge() {
    if (purgeConfirmText.trim() !== 'DELETE' || purging) return;
    purging = true;
    try {
      await apiService.purgeFederationServer(serverId);
      notificationStore.success('Server and all associated data deleted');
      goto('/mesh');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to delete server');
      purging = false;
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
            <span
              class="badge"
              data-status={server.revoked ? 'revoked' : server.disconnectPending ? 'pending' : 'approved'}
              >{server.revoked ? 'Disconnected' : server.disconnectPending ? 'Pending disconnect' : 'Connected'}</span
            >
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
          {#if attempt?.approvedBy && attempt.approvedByUsername}
            <p class="meta"
              >Approved by <Username
                userID={attempt.approvedBy}
                username={attempt.approvedByUsername}
                class="meta-link"
                at
                fire={false}
              /> {#if attempt.approvedAt}{formatRelativeTime(attempt.approvedAt)}{/if}</p
            >
          {/if}
          {#if server.revoked}
            <p class="meta">
              {#if server.revokedBy && server.revokedByUsername}
                Disconnected by <Username
                  userID={server.revokedBy}
                  username={server.revokedByUsername}
                  class="meta-link"
                  at
                  fire={false}
                />
                {#if server.revokedAt}· {formatRelativeTime(server.revokedAt)}{/if}
              {:else if server.revokedAt}
                Disconnected {formatRelativeTime(server.revokedAt)}
              {/if}
            </p>
            {#if server.revokedReason}
              <p class="meta reason">&ldquo;{server.revokedReason}&rdquo;</p>
            {/if}
          {/if}

          {#if server.disconnectPending && !server.revoked}
            <p class="meta">
              {#if server.disconnectRequestedBy && server.disconnectRequestedByUsername}
                Disconnect requested by <Username
                  userID={server.disconnectRequestedBy}
                  username={server.disconnectRequestedByUsername}
                  class="meta-link"
                  at
                  fire={false}
                />
                {#if server.disconnectRequestedAt}· {formatRelativeTime(server.disconnectRequestedAt)}{/if}
              {:else if server.disconnectRequestedAt}
                Disconnect requested {formatRelativeTime(server.disconnectRequestedAt)}
              {/if}
            </p>
            {#if server.disconnectReason}
              <p class="meta reason">&ldquo;{server.disconnectReason}&rdquo;</p>
            {/if}
            {#if confirmBlocked}
              <p class="meta reason">A different admin must confirm this disconnect.</p>
            {/if}
          {/if}

          {#if server.disconnectPending && !server.revoked}
            <div class="approval-actions">
              <button class="btn secondary" disabled={cancelling} on:click={cancelDisconnect}>
                {cancelling ? 'Cancelling…' : 'Cancel request'}
              </button>
              <button
                class="btn danger"
                disabled={confirming || confirmBlocked}
                on:click={confirmDisconnect}
              >
                {confirming ? 'Confirming…' : 'Confirm disconnect'}
              </button>
            </div>
          {:else if !server.revoked}
            <div class="approval-actions">
              <button class="btn danger" disabled={requesting} on:click={openRevokeModal}>
                Disconnect
              </button>
            </div>
          {:else if isRoot}
            <div class="approval-actions">
              <button class="btn danger" disabled={purging} on:click={openPurgeModal}>
                Delete server and all data
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

  {#if showRevokeModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="revoke-server-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissRevokeModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissRevokeModal()}
    >
      <div class="modal">
        <h2 id="revoke-server-title">Request disconnect</h2>
        <p class="modal-lead"
          >Explain why this peer should be disconnected. This is recorded in the server's log. A
          different admin must confirm this request before the peer is actually disconnected and
          notified — root may confirm its own request.</p
        >
        <p class="modal-warning"
          ><strong>This cannot be undone.</strong> There is no reconnect — restoring the connection
          later means a brand new handshake, which this server will treat as a different peer.
          Every client that cached reeds, replies, or profiles from this server will be left with
          permanently broken local references to it.</p
        >
        <label class="field">
          <span>Reason</span>
          <textarea
            bind:value={revokeReason}
            rows="4"
            spellcheck="true"
            placeholder="e.g. No longer an active partner"
          ></textarea>
        </label>
        <div class="modal-actions">
          <button class="btn secondary" disabled={requesting} on:click={dismissRevokeModal}>
            Cancel
          </button>
          <button class="btn danger" disabled={requesting || !revokeReason.trim()} on:click={requestDisconnect}>
            {requesting ? 'Requesting…' : 'Request disconnect'}
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if showPurgeModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="purge-server-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissPurgeModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissPurgeModal()}
    >
      <div class="modal">
        <h2 id="purge-server-title">Delete server and all data</h2>
        <p class="modal-lead"
          >This permanently deletes this server's row and every local reed, reply, echo, and
          identity record associated with it. This cannot be undone.</p
        >
        <label class="field">
          <span>Type DELETE to confirm</span>
          <input type="text" bind:value={purgeConfirmText} autocomplete="off" spellcheck="false" />
        </label>
        <div class="modal-actions">
          <button class="btn secondary" disabled={purging} on:click={dismissPurgeModal}>
            Cancel
          </button>
          <button
            class="btn danger"
            disabled={purging || purgeConfirmText.trim() !== 'DELETE'}
            on:click={purge}
          >
            {purging ? 'Deleting…' : 'Delete permanently'}
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

  .badge[data-status='approved'] {
    color: #27ae60;
  }

  .badge[data-status='revoked'] {
    color: #c0392b;
  }

  .badge[data-status='pending'] {
    color: #b7791f;
  }

  .meta {
    font-size: 0.85rem;
    color: var(--muted);
    margin: 0.15rem 0;
  }

  .meta.reason {
    font-style: italic;
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

  .modal-warning {
    margin: 0 0 1rem 0;
    padding: 0.6rem 0.75rem;
    border: 1px solid #c0392b;
    border-radius: 8px;
    background: rgba(192, 57, 43, 0.08);
    color: #c0392b;
    font-size: 0.85rem;
    line-height: 1.4;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    margin-bottom: 1rem;
  }

  .field textarea,
  .field input[type='text'] {
    font-family: inherit;
    font-size: 0.9rem;
    padding: 0.5rem;
    border-radius: 0.35rem;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
  }

  .field textarea {
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
