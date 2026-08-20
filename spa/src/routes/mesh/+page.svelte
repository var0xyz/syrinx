<script lang="ts">
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import type * as api from '$lib/types/api';
  import { notificationStore } from '$lib/stores/notifications';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import Username from '$lib/components/Username.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  let user: api.User | null = null;
  let isAdmin = false;
  let invitations: api.FederationInvitation[] = [];
  let loading = true;
  let remotePublicKey = '';
  let inviteName = '';
  let creating = false;
  let revokingId: string | null = null;
  let freshConnectionString = '';
  let showConnectionModal = false;
  let showCreateModal = false;
  let showAcceptModal = false;
  let acceptConnectionString = '';
  let accepting = false;

  onMount(async () => {
    user = await authService.getCurrentUser();
    if (!user) return;
    isAdmin = user.role === 'admin' || user.role === 'root';
    if (!isAdmin) {
      loading = false;
      return;
    }
    await refreshList();
    loading = false;
  });

  async function refreshList() {
    try {
      invitations = await apiService.listFederationInvitations();
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
    try {
      const created = await apiService.createFederationInvitation(name, btoa(armor));
      showCreateModal = false;
      freshConnectionString = created.connectionString;
      showConnectionModal = true;
      remotePublicKey = '';
      inviteName = '';
      await refreshList();
      notificationStore.success('Federation invite created.');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to create invite');
    } finally {
      creating = false;
    }
  }

  function openCreateModal() {
    if (creating) return;
    showCreateModal = true;
  }

  function dismissCreateModal() {
    if (creating) return;
    showCreateModal = false;
    remotePublicKey = '';
    inviteName = '';
  }

  async function revokeInvite(inviteId: string) {
    if (revokingId) return;
    if (!confirm('Are you sure you want to revoke this federation invite?')) {
      return;
    }
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
    showAcceptModal = true;
  }

  function dismissAcceptModal() {
    if (accepting) return;
    showAcceptModal = false;
    acceptConnectionString = '';
  }

  async function acceptInvite() {
    const encoded = acceptConnectionString.trim();
    if (!encoded || accepting) return;
    accepting = true;
    try {
      const connectionString = decodeConnectionString(encoded);
      await apiService.attemptFederationConnection(connectionString);
      showAcceptModal = false;
      acceptConnectionString = '';
      notificationStore.success('Connection accepted — awaiting a second admin’s approval.');
    } catch (err) {
      notificationStore.error(err instanceof Error ? err.message : 'Failed to accept connection');
    } finally {
      accepting = false;
    }
  }

  $: freshConnectionEncoded = freshConnectionString
    ? encodeConnectionString(freshConnectionString)
    : '';

  function dismissConnectionModal() {
    showConnectionModal = false;
    freshConnectionString = '';
  }

  async function copyConnectionString(connectionString: string) {
    if (!connectionString) return;
    const encoded = encodeConnectionString(connectionString);
    try {
      await navigator.clipboard.writeText(encoded);
      notificationStore.success('Connection string copied');
    } catch {
      notificationStore.error('Could not copy to clipboard');
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
</script>

<Auth>
  <div class="mesh-container">
    <div class="mesh-content">
      {#if loading}
        <p class="muted">Loading…</p>
      {:else if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        <p class="lead">Federate with other Syrinx instances</p>

        {#if invitations.length === 0}
          <div class="empty-state">
            <div class="empty-icon">🔗</div>
            <h3>No federation invites yet</h3>
            <p>Create an invite to share an encrypted connection string with another server&apos;s admin.</p>
          </div>
        {:else}
          <ul class="invite-list">
            {#each invitations as inv (inv.inviteId)}
              <li class="invite-row">
                <div class="invite-main">
                  <span class="invite-name">{inv.name}</span>
                  <span class="badge-row">
                    <span class="badge" data-status={inv.status}>{statusLabel(inv.status)}</span>
                  </span>
                  <span class="meta"
                    >Created {formatRelativeTime(inv.createdAt)} by <Username
                      userID={inv.createdBy}
                      username={inv.createdByUsername}
                      class="meta-link"
                      at
                      fire={false}
                    /></span
                  >
                  {#if inv.reviewedBy && inv.reviewedByUsername}
                    <span class="meta">
                      {reviewActionLabel(inv.status)}
                      <Username userID={inv.reviewedBy} username={inv.reviewedByUsername} class="meta-link" at fire={false} />
                      {#if inv.reviewedAt}
                        · {formatRelativeTime(inv.reviewedAt)}
                      {/if}
                    </span>
                  {/if}
                </div>
                <div class="invite-actions">
                  {#if inv.status === 'new' && inv.connectionString}
                    <CopyButton
                      ariaLabel="Copy connection string"
                      on:click={() => copyConnectionString(inv.connectionString!)}
                    />
                  {/if}
                  {#if inv.status === 'new'}
                    <button
                      class="btn danger"
                      disabled={revokingId === inv.inviteId}
                      on:click={() => revokeInvite(inv.inviteId)}
                    >
                      {revokingId === inv.inviteId ? 'Revoking…' : 'Revoke'}
                    </button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}

        <button
          class="floating-accept-btn"
          disabled={accepting}
          on:click={openAcceptModal}
          aria-label={accepting ? 'Accepting connection' : 'Accept federation connection'}
        >
          <span class="icon">{accepting ? '…' : '📥'}</span>
        </button>

        <button
          class="floating-create-btn"
          disabled={creating}
          on:click={openCreateModal}
          aria-label={creating ? 'Creating invite' : 'Create federation invite'}
        >
          <span class="icon">{creating ? '…' : '🔗'}</span>
        </button>
      {/if}
    </div>

    <BottomToolbar currentPage="mesh" />
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
          Label this invite so you can tell who it is for, then paste the remote server&apos;s OpenPGP public key (exchanged out of band).
        </p>
        <label class="field">
          <span>Name</span>
          <input
            type="text"
            bind:value={inviteName}
            maxlength="255"
            placeholder="e.g. Acme staging"
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
          Paste the connection string another server&apos;s admin shared with you out of band.
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
            <label for="connection-share">Share code</label>
            <CopyButton
              ariaLabel="Copy connection string"
              on:click={() => copyConnectionString(freshConnectionString)}
            />
          </div>
          <input
            id="connection-share"
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

  .lead {
    margin: 0 0 1rem 0;
    color: var(--muted);
    font-size: 0.9rem;
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

  .badge[data-status='accepted'] {
    color: #d68910;
  }

  .badge[data-status='approved'] {
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

  .floating-create-btn,
  .floating-accept-btn {
    position: fixed;
    bottom: 5rem;
    right: 1.5rem;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .floating-accept-btn {
    bottom: 9.5rem;
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .floating-create-btn:hover:not(:disabled),
  .floating-accept-btn:hover:not(:disabled) {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-create-btn:disabled,
  .floating-accept-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .floating-create-btn .icon,
  .floating-accept-btn .icon {
    font-size: 1.5rem;
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
