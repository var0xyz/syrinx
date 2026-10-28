<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import type * as api from '$lib/types/api';
  import { notificationStore } from '$lib/stores/notifications';
  import { serverInfo } from '$lib/services/serverInfo';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import FederationInfoModal from '$lib/components/FederationInfoModal.svelte';
  import Username from '$lib/components/Username.svelte';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { adminPermissions, adminTabs, canMail, holds } from '$lib/stores/adminPermissions';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  $: viewer = $page.data?.user;
  $: isAdmin = viewer?.role === 'admin' || viewer?.role === 'root';
  let invitations: api.FederationInvitation[] = [];
  let attempts: api.FederationAttempt[] = [];
  let servers: api.FederationServer[] = [];
  let loaded = false;
  let remotePublicKey = '';
  let inviteName = '';
  let creating = false;
  let revokingId: string | null = null;
  let revokeTarget: api.FederationInvitation | null = null;
  let freshConnectionString = '';
  let showConnectionModal = false;
  let showCreateModal = false;
  let showAcceptModal = false;
  let acceptConnectionString = '';
  let accepting = false;
  let modalError = '';
  let showFederationInfo = false;
  let showActions = false;

  function runAction(action: () => void) {
    showActions = false;
    action();
  }

  onMount(async () => {
    if (!isAdmin) return;
    await refreshList();
    loaded = true;
  });

  async function refreshList() {
    try {
      const list = await apiService.listFederation();
      invitations = list.invitations;
      attempts = list.attempts;
      servers = list.servers;
    } catch (err) {
      console.error('[mesh]', err);
      notificationStore.error(err instanceof Error ? err.message : 'Failed to load federation invites');
    }
  }

  async function createInvite() {
    const armor = remotePublicKey.trim();
    const name = inviteName.trim();
    if (!armor || !name || creating) return;
    creating = true;
    modalError = '';
    try {
      const created = await apiService.createFederationInvitation(name, armor);
      showCreateModal = false;
      freshConnectionString = created.connectionString;
      showConnectionModal = true;
      remotePublicKey = '';
      inviteName = '';
      await refreshList();
      notificationStore.success('Federation invite created.');
    } catch (err) {
      modalError = err instanceof Error ? err.message : 'Failed to create invite';
    } finally {
      creating = false;
    }
  }

  function openCreateModal() {
    if (creating) return;
    showAcceptModal = false;
    modalError = '';
    showCreateModal = true;
  }

  function dismissCreateModal() {
    if (creating) return;
    showCreateModal = false;
    remotePublicKey = '';
    inviteName = '';
    modalError = '';
  }

  function requestRevoke(inv: api.FederationInvitation) {
    if (revokingId) return;
    revokeTarget = inv;
  }

  async function confirmRevoke() {
    if (!revokeTarget) return;
    const inviteId = revokeTarget.inviteId;
    revokeTarget = null;
    revokingId = inviteId;
    try {
      await apiService.revokeFederationInvitation(inviteId);
      await refreshList();
      notificationStore.success('Invite revoked');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to revoke invite');
    } finally {
      revokingId = null;
    }
  }

  function encodeConnectionString(connectionString: string): string {
    const bytes = new TextEncoder().encode(connectionString);
    let binary = '';
    for (const byte of bytes) {
      binary += String.fromCharCode(byte);
    }
    return btoa(binary);
  }

  function decodeConnectionString(encoded: string): string {
    const binary = atob(encoded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return new TextDecoder().decode(bytes);
  }

  function openAcceptModal() {
    if (accepting) return;
    showCreateModal = false;
    modalError = '';
    showAcceptModal = true;
  }

  function dismissAcceptModal() {
    if (accepting) return;
    showAcceptModal = false;
    acceptConnectionString = '';
    modalError = '';
  }

  async function acceptInvite() {
    const encoded = acceptConnectionString.trim();
    if (!encoded || accepting) return;
    accepting = true;
    modalError = '';
    let connectionString

    try {
      connectionString = decodeConnectionString(encoded);
    } catch (err) {
      modalError = "Invalid connection string"
      accepting = false;
      return
    }

    try {
      await apiService.attemptFederationConnection(connectionString);
      showAcceptModal = false;
      acceptConnectionString = '';
      notificationStore.success('Connection accepted — awaiting a second admin’s approval.');
    } catch (err) {
      modalError = err instanceof Error ? err.message : 'Failed to accept connection';
    } finally {
      accepting = false;
      // The server records the attempt even when the handshake fails.
      await refreshList();
    }
  }

  $: freshConnectionEncoded = freshConnectionString
    ? encodeConnectionString(freshConnectionString)
    : '';

  function dismissConnectionModal() {
    showConnectionModal = false;
    freshConnectionString = '';
  }

  async function copyOwnPublicKey() {
    const keyId = $serverInfo?.serverKeyId;
    if (!keyId) return;
    try {
      const key = await apiService.getPublicKey(keyId);
      await navigator.clipboard.writeText(key.armor);
      notificationStore.success('Server public key copied');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Could not copy server public key');
    }
  }

  async function copyConnectionString(connectionString: string): Promise<boolean> {
    if (!connectionString) return false;
    const encoded = encodeConnectionString(connectionString);
    try {
      await navigator.clipboard.writeText(encoded);
      notificationStore.success('Connection string copied');
      return true;
    } catch {
      notificationStore.error('Could not copy to clipboard');
      return false;
    }
  }

  function statusLabel(status: api.FederationInvitation['status']) {
    if (status === 'accepted') return 'Accepted';
    if (status === 'approved') return 'Approved';
    if (status === 'rejected') return 'Rejected';
    if (status === 'canceled') return 'Canceled';
    if (status === 'revoked') return 'Revoked';
    return 'Pending';
  }

  function reviewActionLabel(status: api.FederationInvitation['status']) {
    if (status === 'approved') return 'Approved by';
    if (status === 'rejected') return 'Rejected by';
    if (status === 'canceled') return 'Canceled by';
    if (status === 'revoked') return 'Revoked by';
    return 'Reviewed by';
  }

  function attemptStatusLabel(status: api.FederationAttempt['status']) {
    if (status === 'approved') return 'Approved';
    if (status === 'rejected') return 'Rejected';
    return 'Pending';
  }

  /** Reuses the invitation badge's color scheme (same status vocabulary). */
  function attemptBadgeStatus(status: api.FederationAttempt['status']) {
    if (status === 'approved') return 'approved';
    if (status === 'rejected') return 'rejected';
    return 'new';
  }

  type PeerRow =
    | { kind: 'attempt'; key: string; createdAt: number; item: api.FederationAttempt }
    | { kind: 'server'; key: string; createdAt: number; item: api.FederationServer };

  // Attempts and servers are the same lifecycle (pending/approved connection),
  // just rendered as one list instead of two separately-headed sections.
  $: peerRows = [
    ...attempts.map((item): PeerRow => ({ kind: 'attempt', key: `att-${item.attemptId}`, createdAt: item.createdAt, item })),
    ...servers.map((item): PeerRow => ({ kind: 'server', key: `srv-${item.serverId}`, createdAt: item.createdAt, item })),
  ].sort((a, b) => b.createdAt - a.createdAt);

  function rowHref(row: PeerRow): string {
    return row.kind === 'attempt'
      ? `/mesh/attempt/${encodeURIComponent(row.item.attemptId)}`
      : `/mesh/peer/${encodeURIComponent(row.item.serverId)}`;
  }
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && (showActions = false)} />

<Auth>
  <SideNav currentPage="admin" />
  <div class="mesh-container">
    <SectionTabs {tabs} active="mesh" />
    <div class="mesh-content">
      {#if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        {#if loaded && invitations.length === 0 && attempts.length === 0 && servers.length === 0}
          <div class="empty-state">
            <div class="empty-icon">🔗</div>
            <h3>No federation invites yet</h3>
            <p>Create an invite to share an encrypted connection string with another server&apos;s admin.</p>
          </div>
        {:else if invitations.length > 0}
          <ul class="invite-list">
            {#each invitations as inv (inv.inviteId)}
              <li class="invite-row">
                <div class="invite-main">
                  <span class="invite-name">{inv.name}</span>
                  <span class="badge-row">
                    <span class="badge" data-status={inv.status}>{statusLabel(inv.status)}</span>
                  </span>
                  <span class="meta"
                    >Created {formatRelativeTime(fromUnix(inv.createdAt))} by <Username
                      userID={inv.createdBy}
                      class="meta-link"
                      at
                      fire={false}
                    /></span
                  >
                  {#if inv.reviewedBy}
                    <span class="meta">
                      {reviewActionLabel(inv.status)}
                      <Username userID={inv.reviewedBy} class="meta-link" at fire={false} />
                      {#if inv.reviewedAt}
                        · {formatRelativeTime(fromUnix(inv.reviewedAt))}
                      {/if}
                    </span>
                  {/if}
                  {#if inv.status === 'new' && inv.connectionString}
                    <span class="pending-hint">Send the connection string to the other server&apos;s admin.</span>
                  {/if}
                </div>
                <div class="invite-actions">
                  {#if inv.status === 'new' && inv.connectionString}
                    <CopyButton
                      ariaLabel="Copy connection string"
                      copy={() => copyConnectionString(inv.connectionString!)}
                    />
                  {/if}
                  {#if inv.status === 'new' && holds(viewer, $adminPermissions, 'mesh.invite')}
                    <button
                      class="btn danger"
                      disabled={revokingId === inv.inviteId}
                      on:click={() => requestRevoke(inv)}
                    >
                      {revokingId === inv.inviteId ? 'Revoking…' : 'Revoke'}
                    </button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}

        {#if peerRows.length > 0}
          <ul class="invite-list">
            {#each peerRows as row (row.key)}
              <li>
                <div
                  class="invite-row clickable"
                  role="button"
                  tabindex="0"
                  on:click={() => goto(rowHref(row))}
                  on:keydown={(e) => e.key === 'Enter' && goto(rowHref(row))}
                >
                  {#if row.kind === 'attempt'}
                    <div class="invite-main">
                      <span class="invite-name">{row.item.remoteServerName}</span>
                      <span class="badge-row">
                        <span class="badge" data-status={attemptBadgeStatus(row.item.status)}>
                          {attemptStatusLabel(row.item.status)}
                        </span>
                      </span>
                      <span class="meta">{row.item.baseUrl}</span>
                      <span class="meta">Started {formatRelativeTime(fromUnix(row.item.createdAt))}</span>
                    </div>
                  {:else}
                    <div class="invite-main">
                      <span class="invite-name">{row.item.name}</span>
                      <span class="badge-row">
                        <span
                          class="badge"
                          data-status={row.item.revoked
                            ? 'revoked'
                            : row.item.disconnectPending
                              ? 'pending'
                              : row.item.established
                                ? 'connected'
                                : 'accepted'}
                        >
                          {row.item.revoked
                            ? 'Disconnected'
                            : row.item.disconnectPending
                              ? 'Pending disconnect'
                              : row.item.established
                                ? 'Connected'
                                : 'Awaiting their approval'}
                        </span>
                      </span>
                      {#if row.item.baseUrl}
                        <span class="meta">{row.item.baseUrl}</span>
                      {/if}
                      <span class="meta">Added {formatRelativeTime(fromUnix(row.item.createdAt))}</span>
                    </div>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}

        {#if showActions}
          <button
            type="button"
            class="actions-scrim"
            aria-label="Close actions"
            on:click={() => (showActions = false)}
          ></button>
        {/if}

        <div class="speed-dial">
          {#if showActions}
            <ul id="mesh-actions" class="speed-dial-actions">
              <li>
                <button
                  type="button"
                  class="speed-dial-action"
                  disabled={!$serverInfo?.serverKeyId}
                  on:click={() => runAction(copyOwnPublicKey)}
                >
                  <span class="action-label">Copy this server’s public key</span>
                  <span class="action-icon"><span class="icon-copy" aria-hidden="true"></span></span>
                </button>
              </li>
              {#if holds(viewer, $adminPermissions, 'mesh.connect')}
                <li>
                  <button type="button" class="speed-dial-action" on:click={() => runAction(openAcceptModal)}>
                    <span class="action-label">Paste a connection string</span>
                    <span class="action-icon"><span class="icon-connect" aria-hidden="true"></span></span>
                  </button>
                </li>
              {/if}
              {#if holds(viewer, $adminPermissions, 'mesh.invite')}
                <li>
                  <button
                    type="button"
                    class="speed-dial-action"
                    disabled={creating}
                    on:click={() => runAction(openCreateModal)}
                  >
                    <span class="action-label">Invite a server to connect</span>
                    <span class="action-icon"><span class="icon-link-small" aria-hidden="true"></span></span>
                  </button>
                </li>
              {/if}
            </ul>
          {/if}

          <button
            type="button"
            class="floating-create-btn"
            class:open={showActions}
            aria-label="Federation actions"
            aria-haspopup="true"
            aria-expanded={showActions}
            aria-controls="mesh-actions"
            on:click={() => (showActions = !showActions)}
          >
            <span class="icon icon-link"></span>
          </button>
        </div>

        <div class="desktop-actions">
          <button
            class="btn secondary"
            disabled={!$serverInfo?.serverKeyId}
            on:click={copyOwnPublicKey}
          >
            <span class="icon-copy" aria-hidden="true"></span>
            Copy this server’s public key
          </button>
          {#if holds(viewer, $adminPermissions, 'mesh.connect')}
            <button class="btn secondary" on:click={openAcceptModal}>
              <span class="icon-connect" aria-hidden="true"></span>
              Paste a connection string
            </button>
          {/if}
          {#if holds(viewer, $adminPermissions, 'mesh.invite')}
            <button class="btn primary" disabled={creating} on:click={openCreateModal}>
              <span class="icon-link-small" aria-hidden="true"></span>
              {creating ? 'Creating…' : 'Invite a server to connect'}
            </button>
          {/if}
        </div>

        <button type="button" class="explain-btn" on:click={() => (showFederationInfo = true)}>
          About federation
          <span class="info-icon" aria-hidden="true"></span>
        </button>
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>

  {#if showCreateModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-invite-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissCreateModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissCreateModal()}
    >
      <div class="modal">
        <h2 id="create-invite-title">New connection</h2>
        <p class="modal-lead">
          Paste the remote server&apos;s OpenPGP public key (exchanged out-of-band).
        </p>
        <label class="field">
          <span>Name</span>
          <input
            type="text"
            bind:value={inviteName}
            maxlength="255"
            placeholder="e.g. Mom's server"
          />
        </label>
        <label class="field">
          <span>Remote server public key</span>
          <textarea
            bind:value={remotePublicKey}
            rows="8"
            spellcheck="false"
            placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----"
          ></textarea>
        </label>
        {#if modalError}
          <div class="modal-error">{modalError}</div>
        {/if}
        <div class="modal-actions">
          <button class="btn secondary" disabled={creating} on:click={dismissCreateModal}>
            Cancel
          </button>
          <button
            class="btn primary"
            disabled={creating || !remotePublicKey.trim() || !inviteName.trim()}
            on:click={createInvite}
          >
            {creating ? 'Creating…' : 'Create invite'}
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if showAcceptModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="accept-connection-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissAcceptModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissAcceptModal()}
    >
      <div class="modal">
        <h2 id="accept-connection-title">Accept connection</h2>
        <p class="modal-lead">
          Paste the connection string another server&apos;s admin shared with you out-of-band.
        </p>
        <label class="field">
          <span>Connection string</span>
          <textarea
            bind:value={acceptConnectionString}
            rows="8"
            spellcheck="false"
            placeholder="Paste the share code here"
          ></textarea>
        </label>
        {#if modalError}
          <div class="modal-error">{modalError}</div>
        {/if}
        <div class="modal-actions">
          <button class="btn secondary" disabled={accepting} on:click={dismissAcceptModal}>
            Cancel
          </button>
          <button
            class="btn primary"
            disabled={accepting || !acceptConnectionString.trim()}
            on:click={acceptInvite}
          >
            {accepting ? 'Accepting…' : 'Accept'}
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if showConnectionModal}
    <div
      class="modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="connection-title"
      tabindex="-1"
      on:click={(e) => e.target === e.currentTarget && dismissConnectionModal()}
      on:keydown={(e) => e.key === 'Escape' && dismissConnectionModal()}
    >
      <div class="modal">
        <h2 id="connection-title">Connection string</h2>
        <p class="modal-lead">Share this with the remote admin. You can copy it again from the invite list while it is still pending.</p>
        <div class="connection-field">
          <div class="connection-label">
            <label for="connection-string">Connection string</label>
            <CopyButton
              ariaLabel="Copy connection string"
              copy={() => copyConnectionString(freshConnectionString)}
            />
          </div>
          <input
            id="connection-string"
            class="connection-output"
            type="text"
            readonly
            value={freshConnectionEncoded}
          />
        </div>
        <div class="modal-actions">
          <button class="btn primary" on:click={dismissConnectionModal}>Done</button>
        </div>
      </div>
    </div>
  {/if}

  {#if revokeTarget}
    <ConfirmDialog
      title="Revoke federation invite?"
      message={`This will revoke the invite "${revokeTarget.name}". The connection string will no longer work.`}
      on:confirm={confirmRevoke}
      on:cancel={() => (revokeTarget = null)}
    />
  {/if}

  <FederationInfoModal open={showFederationInfo} on:close={() => (showFederationInfo = false)} />
</Auth>

<style>
  .mesh-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .mesh-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .mesh-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .mesh-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  /* The speed dial covers these on mobile. */
  .desktop-actions {
    display: none;
  }

  @media (min-width: 768px) {
    .desktop-actions {
      display: flex;
      justify-content: center;
      flex-wrap: wrap;
      gap: 0.5rem;
      margin-top: 1rem;
    }

    .desktop-actions .btn {
      width: auto;
      display: flex;
      align-items: center;
      gap: 0.25rem;
    }

    .desktop-actions .icon-connect,
    .desktop-actions .icon-copy,
    .desktop-actions .icon-link-small {
      width: 1rem;
      height: 1rem;
    }
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

  .invite-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .invite-list + .invite-list {
    margin-top: 0.75rem;
  }

  .invite-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.85rem 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .invite-row.clickable {
    cursor: pointer;
  }

  .invite-main {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .invite-name {
    font-weight: 600;
    color: var(--fg);
  }

  .invite-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-shrink: 0;
  }

  .badge {
    align-self: flex-start;
    font-size: 0.75rem;
    font-weight: 600;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: var(--input-bg);
  }

  .badge-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .badge[data-status='new'] {
    color: var(--primary);
  }

  .badge[data-status='accepted'],
  .badge[data-status='pending'] {
    color: #d68910;
  }

  .badge[data-status='approved'] {
    color: #2980b9;
  }

  .badge[data-status='connected'] {
    color: #27ae60;
  }

  .badge[data-status='rejected'],
  .badge[data-status='canceled'],
  .badge[data-status='revoked'] {
    color: var(--muted);
  }

  .meta {
    font-size: 0.85rem;
    color: var(--muted);
  }

  .pending-hint {
    font-size: 0.85rem;
    color: var(--fg);
  }

  :global(.meta .meta-link) {
    color: var(--primary);
    text-decoration: none;
  }

  .explain-btn {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    width: auto;
    margin: 1rem auto 0;
    padding: 0;
    background: none;
    border: none;
    font-size: 0.8rem;
    color: var(--muted);
    cursor: pointer;
  }

  .explain-btn:hover {
    color: var(--fg);
  }

  .info-icon {
    display: inline-block;
    width: 0.9rem;
    height: 0.9rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/info-16.svg');
    mask-image: url('/icons/info-16.svg');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .error {
    color: #b91c1c;
  }

  .speed-dial {
    position: fixed;
    bottom: 5rem;
    right: 1.5rem;
    z-index: 1000;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 1rem;
  }

  .actions-scrim {
    position: fixed;
    inset: 0;
    z-index: 999;
    padding: 0;
    border: none;
    background: transparent;
    cursor: default;
  }

  .speed-dial-actions {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.5rem;
  }

  .speed-dial-action {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    /* Lines the 40px icon up with the 56px floating button's center. */
    margin-right: 0.5rem;
    padding: 0;
    background: none;
    border: none;
    color: var(--fg);
    cursor: pointer;
  }

  .speed-dial-action:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .action-label {
    padding: 0.25rem 0.5rem;
    border-radius: 4px;
    background: var(--bg);
    font-size: 0.85rem;
    white-space: nowrap;
  }

  .action-icon {
    width: 40px;
    height: 40px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg);
    border: 1px solid var(--border);
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.15);
  }

  .speed-dial-action:hover:not(:disabled) .action-icon {
    background: var(--input-bg);
  }

  .floating-create-btn {
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .floating-create-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-create-btn.open .icon-link {
    transform: rotate(45deg);
  }

  /* Floating buttons are a mobile pattern; desktop has .desktop-actions. */
  @media (min-width: 768px) {
    .speed-dial,
    .actions-scrim {
      display: none;
    }
  }

  .floating-create-btn .icon.icon-link {
    display: inline-block;
    width: 1.5rem;
    height: 1.5rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/link-24.svg');
    mask-image: url('/icons/link-24.svg');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
    transition: transform 0.2s ease;
  }

  .icon-connect,
  .icon-copy,
  .icon-link-small {
    display: inline-block;
    width: 1.25rem;
    height: 1.25rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .icon-connect {
    -webkit-mask-image: url('/icons/connect-24.svg');
    mask-image: url('/icons/connect-24.svg');
  }

  .icon-copy {
    -webkit-mask-image: url('/icons/copy-24.svg');
    mask-image: url('/icons/copy-24.svg');
  }

  .icon-link-small {
    -webkit-mask-image: url('/icons/link-24.svg');
    mask-image: url('/icons/link-24.svg');
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    margin-bottom: 1rem;
  }

  .field textarea,
  .field input[type='text'] {
    font-family: ui-monospace, monospace;
    font-size: 0.85rem;
    padding: 0.5rem;
    border-radius: 0.35rem;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
  }

  .field input[type='text'] {
    font-family: inherit;
  }

  .field textarea {
    resize: vertical;
    overflow: auto;
    white-space: nowrap;
  }

  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    /* Above .speed-dial (z-index: 1000) so the modal covers it
       instead of the button floating over it. */
    z-index: 1100;
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

  .connection-field {
    margin-bottom: 1rem;
  }

  .connection-label {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    margin-bottom: 0.35rem;
  }

  .connection-label label {
    font-size: 0.9rem;
    font-weight: 500;
  }

  .connection-output {
    width: 100%;
    font-family: ui-monospace, monospace;
    font-size: 0.85rem;
    padding: 0.5rem;
    border-radius: 0.35rem;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .modal-error {
    margin: 0 0 1rem 0;
    padding: 0.6rem 0.75rem;
    border-radius: 0.35rem;
    border: 1px solid #e0837c;
    background: color-mix(in srgb, #e0837c 12%, transparent);
    color: #c0392b;
    font-size: 0.85rem;
    line-height: 1.4;
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
