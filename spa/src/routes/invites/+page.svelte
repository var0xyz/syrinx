<script lang="ts">
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import type * as api from '$lib/types/api';
  import {
    createSignedInvite,
    inviteShareURL,
    refreshPendingInviteStatuses,
    revokeLocalInvite,
  } from '$lib/services/invites';
  import { invitesRepository } from '$lib/repositories/invites';
  import {
    isSignupClosed,
    refreshServerInfo,
    serverInfo,
  } from '$lib/services/serverInfo';
  import { notificationStore } from '$lib/stores/notifications';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  let user = null;
  let invites: api.Invite[] = [];
  let refreshingIds: string[] = [];
  let creating = false;
  let revokingId: string | null = null;
  let freshShareURL = '';
  let showFreshLink = false;
  let isAdmin = false;
  let showRoleModal = false;
  let selectedGrantRole: 'user' | 'admin' = 'user';

  $: maxInvites = $serverInfo?.maxInvitesPerUser ?? -1;
  $: usedInvites = invites.length;
  $: atQuota = maxInvites !== -1 && usedInvites >= maxInvites;
  $: canCreate = !$isSignupClosed && !atQuota;
  $: invitesRemaining =
    maxInvites === -1 ? null : Math.max(0, maxInvites - usedInvites);
  $: quotaSummary =
    maxInvites === -1
      ? 'No invite limit on this server.'
      : invitesRemaining === 0
        ? `You’ve used all ${maxInvites} invite${maxInvites === 1 ? '' : 's'}. Revoking unused invites does not free quota.`
        : `You can create ${maxInvites} invite${maxInvites === 1 ? '' : 's'} — ${invitesRemaining} left.`;

  onMount(async () => {
    user = await authService.getCurrentUser();
    if (!user) return;

    isAdmin = user.role === 'admin' || user.role === 'root';

    // Show local invites immediately so the toolbar stays mounted.
    invites = await invitesRepository.getAll();
    void refreshServerInfo();
    await refreshPendingStatuses();
  });

  function applyInviteUpdate(updated: api.Invite) {
    refreshingIds = refreshingIds.filter((id) => id !== updated.id);
    const idx = invites.findIndex((invite) => invite.id === updated.id);
    if (idx === -1) {
      invites = [updated, ...invites];
    } else {
      invites[idx] = updated;
      invites = invites;
    }
  }

  async function refreshPendingStatuses() {
    const pendingIds = invites
      .filter((invite) => invite.status === 'pending')
      .map((invite) => invite.id);
    if (pendingIds.length === 0) return;

    refreshingIds = pendingIds;
    try {
      invites = await refreshPendingInviteStatuses(applyInviteUpdate);
    } catch (err) {
      console.error(err);
      notificationStore.error(
        err instanceof Error ? err.message : 'Failed to refresh invite status'
      );
    } finally {
      refreshingIds = [];
    }
  }

  function onCreateClick() {
    if (!canCreate || creating) return;
    if (isAdmin) {
      selectedGrantRole = 'user';
      showRoleModal = true;
      return;
    }
    void createInvite(false);
  }

  function dismissRoleModal() {
    showRoleModal = false;
    selectedGrantRole = 'user';
  }

  function confirmRoleAndCreate() {
    const grantAdmin = selectedGrantRole === 'admin';
    dismissRoleModal();
    void createInvite(grantAdmin);
  }

  async function createInvite(grantAdmin: boolean) {
    if (!canCreate || creating) return;
    creating = true;
    try {
      const created = await createSignedInvite(grantAdmin);
      if (created.secret && user?.id) {
        freshShareURL = inviteShareURL(created.id, created.secret, user.id);
        showFreshLink = true;
      }
      invites = await invitesRepository.getAll();
    } catch (err) {
      console.error(err);
      notificationStore.error(
        err instanceof Error ? err.message : 'Failed to create invite'
      );
    } finally {
      creating = false;
    }
  }

  async function revokeInvite(id: string) {
    if (revokingId) return;
    if (!confirm('Are you sure you want to revoke this invite?')) {
      return;
    }
    revokingId = id;
    try {
      await revokeLocalInvite(id);
      invites = await invitesRepository.getAll();
      notificationStore.success('Invite revoked');
    } catch (err) {
      console.error(err);
      notificationStore.error(
        err instanceof Error ? err.message : 'Failed to revoke invite'
      );
    } finally {
      revokingId = null;
    }
  }

  function shareURL(invite: api.Invite): string | null {
    if (!invite.secret || !user?.id) return null;
    return inviteShareURL(invite.id, invite.secret, user.id);
  }

  async function copyLink(url: string) {
    try {
      await navigator.clipboard.writeText(url);
      notificationStore.success('Invite link copied');
    } catch (err) {
      console.error(err);
      notificationStore.error('Failed to copy invite link');
    }
  }

  async function copyFreshLink() {
    await copyLink(freshShareURL);
  }

  function dismissFreshLink() {
    showFreshLink = false;
    freshShareURL = '';
  }

  function statusLabel(status: string) {
    if (status === 'claimed') return 'Claimed';
    if (status === 'revoked') return 'Revoked';
    return 'Pending';
  }
</script>

<Auth>
  <div class="invites-container">
    <div class="invites-content">
      {#if canCreate}
        <button
          class="floating-create-btn"
          disabled={creating}
          on:click={onCreateClick}
          aria-label={creating ? 'Creating invite' : 'Create invite'}
        >
          <span class="icon">{creating ? '…' : '✉️'}</span>
        </button>
      {/if}

      {#if !$isSignupClosed}
        <p class="quota">{quotaSummary}</p>
      {/if}

      {#if $isSignupClosed}
        <div class="empty-state">
          <div class="empty-icon">🚫</div>
          <h3>Signups are closed</h3>
          <p>This server isn’t accepting new invites right now.</p>
        </div>
      {:else if invites.length === 0}
        <div class="empty-state">
          <div class="empty-icon">📭</div>
          <h3>No invites yet</h3>
          <p>
            Create an invite to share a signup link with someone you trust.
          </p>
        </div>
      {:else}
        <ul class="invite-list">
          {#each invites as invite (invite.id)}
            <li class="invite-row">
              <div class="invite-main">
                <span class="badge-row">
                  <span class="badge" data-status={invite.status}
                    >{statusLabel(invite.status)}</span
                  >
                  {#if invite.grantedRole === 'admin'}
                    <span class="badge admin-badge">Admin</span>
                  {/if}
                  {#if refreshingIds.includes(invite.id)}
                    <span
                      class="status-spinner"
                      aria-label="Refreshing invite status"
                    ></span>
                  {/if}
                </span>
                <span class="meta"
                  >Created {formatRelativeTime(invite.createdAt)}</span
                >
                {#if invite.status === 'claimed' && invite.claimedBy}
                  <span class="meta">
                    Claimed by
                    <a href="/profile/{invite.claimedBy.id}"
                      >@{invite.claimedBy.username}</a
                    >
                  </span>
                {/if}
              </div>
              <div class="invite-actions">
                {#if invite.status === 'pending' && shareURL(invite)}
                  <CopyButton
                    ariaLabel="Copy invite link"
                    on:click={() => copyLink(shareURL(invite)!)}
                  />
                {/if}
                {#if invite.status === 'pending'}
                  <button
                    class="btn danger"
                    disabled={revokingId === invite.id}
                    on:click={() => revokeInvite(invite.id)}
                  >
                    {revokingId === invite.id ? 'Revoking…' : 'Revoke'}
                  </button>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    {#if showRoleModal}
      <div
        class="modal-backdrop"
        role="dialog"
        aria-modal="true"
        aria-labelledby="invite-role-title"
        tabindex="-1"
        on:click={(e) => e.target === e.currentTarget && dismissRoleModal()}
        on:keydown={(e) => e.key === 'Escape' && dismissRoleModal()}
      >
        <div class="modal role-modal">
          <h2 id="invite-role-title">New invite</h2>
          <p class="role-modal-lead">Choose what role the person gets when they sign up.</p>

          <div class="role-options" role="radiogroup" aria-labelledby="invite-role-title">
            <button
              type="button"
              role="radio"
              aria-checked={selectedGrantRole === 'user'}
              class="role-option"
              class:selected={selectedGrantRole === 'user'}
              on:click={() => (selectedGrantRole = 'user')}
            >
              <span class="role-indicator" aria-hidden="true"></span>
              <span class="role-option-body">
                <span class="role-option-title">User</span>
                <span class="role-option-desc">Standard member — can post and invite other users.</span>
              </span>
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={selectedGrantRole === 'admin'}
              class="role-option role-option-admin"
              class:selected={selectedGrantRole === 'admin'}
              on:click={() => (selectedGrantRole = 'admin')}
            >
              <span class="role-indicator" aria-hidden="true"></span>
              <span class="role-option-body">
                <span class="role-option-title">Admin</span>
                <span class="role-option-desc">Can invite admins and perform operator actions on this server.</span>
              </span>
            </button>
          </div>

          <div class="modal-actions">
            <button type="button" class="btn secondary" on:click={dismissRoleModal}>
              Cancel
            </button>
            <button
              type="button"
              class="btn primary"
              disabled={creating}
              on:click={confirmRoleAndCreate}
            >
              {creating ? 'Creating…' : 'Create invite'}
            </button>
          </div>
        </div>
      </div>
    {/if}

    {#if showFreshLink}
      <div
        class="modal-backdrop"
        role="dialog"
        aria-modal="true"
        aria-labelledby="fresh-invite-title"
        tabindex="-1"
        on:click={(e) => e.target === e.currentTarget && dismissFreshLink()}
        on:keydown={(e) => e.key === 'Escape' && dismissFreshLink()}
      >
        <div class="modal">
          <h2 id="fresh-invite-title">Invite link ready</h2>
          <div class="link-row">
            <code class="share-url">{freshShareURL}</code>
            <CopyButton
              ariaLabel="Copy invite link"
              on:click={copyFreshLink}
            />
          </div>
          <button class="btn primary" on:click={dismissFreshLink}>Done</button>
        </div>
      </div>
    {/if}

    <BottomToolbar currentPage="invites" />
  </div>
</Auth>

<style>
  .invites-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .invites-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .quota {
    margin: 0 0 1rem 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .floating-create-btn {
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

  .floating-create-btn:hover:not(:disabled) {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-create-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .floating-create-btn .icon {
    font-size: 1.5rem;
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

  .status-spinner {
    width: 0.85rem;
    height: 0.85rem;
    border: 2px solid var(--border);
    border-top-color: var(--primary);
    border-radius: 50%;
    animation: invite-spin 0.8s linear infinite;
    flex-shrink: 0;
  }

  @keyframes invite-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .badge[data-status='pending'] {
    color: var(--primary);
  }

  .badge[data-status='claimed'] {
    color: #27ae60;
  }

  .badge[data-status='revoked'] {
    color: var(--muted);
  }

  .admin-badge {
    color: #8e44ad;
    background: rgba(142, 68, 173, 0.12);
  }

  .meta {
    font-size: 0.85rem;
    color: var(--muted);
  }

  .meta a {
    color: var(--primary);
    text-decoration: none;
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

  .role-modal-lead {
    margin: 0 0 1rem 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.4;
  }

  .role-options {
    border: none;
    margin: 0 0 1.25rem 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.65rem;
  }

  .role-option {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    width: 100%;
    padding: 0.85rem 1rem;
    border: 2px solid var(--border);
    border-radius: 10px;
    background: var(--input-bg);
    color: var(--fg);
    font: inherit;
    font-weight: normal;
    text-align: left;
    cursor: pointer;
    transition: border-color 0.15s ease, background 0.15s ease;
  }

  .role-option:hover {
    background: var(--surface);
  }

  .role-option:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
  }

  .role-indicator {
    width: 1.125rem;
    height: 1.125rem;
    margin-top: 0.15rem;
    flex-shrink: 0;
    border: 2px solid var(--muted);
    border-radius: 50%;
    position: relative;
    box-sizing: border-box;
  }

  .role-option.selected .role-indicator {
    border-color: var(--primary);
  }

  .role-option.selected .role-indicator::after {
    content: '';
    position: absolute;
    inset: 3px;
    border-radius: 50%;
    background: var(--primary);
  }

  .role-option.selected {
    border-color: var(--primary);
    background: var(--surface);
  }

  .role-option-admin.selected {
    border-color: #8e44ad;
  }

  .role-option-admin.selected .role-indicator {
    border-color: #8e44ad;
  }

  .role-option-admin.selected .role-indicator::after {
    background: #8e44ad;
  }

  .role-option-body {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
  }

  .role-option-title {
    font-weight: 600;
    color: var(--fg);
  }

  .role-option-desc {
    font-size: 0.85rem;
    color: var(--muted);
    line-height: 1.35;
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

  .link-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 1rem;
  }

  .share-url {
    flex: 1;
    font-size: 0.8rem;
    word-break: break-all;
    background: var(--input-bg);
    padding: 0.5rem;
    border-radius: 6px;
  }

  @media (max-width: 768px) {
    .invites-content {
      padding: 0.5rem;
    }
  }
</style>
