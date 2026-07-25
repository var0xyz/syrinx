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
  let loading = true;
  let invites: api.Invite[] = [];
  let creating = false;
  let revokingId: string | null = null;
  let freshShareURL = '';
  let showFreshLink = false;

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
    if (!user) {
      loading = false;
      return;
    }
    await refreshServerInfo();
    await loadInvites();
    loading = false;
  });

  async function loadInvites() {
    try {
      invites = await refreshPendingInviteStatuses();
    } catch (err) {
      console.error(err);
      try {
        invites = await invitesRepository.getAll();
      } catch {
        invites = [];
      }
      notificationStore.error(
        err instanceof Error ? err.message : 'Failed to load invites'
      );
    }
  }

  async function createInvite() {
    if (!canCreate || creating) return;
    creating = true;
    try {
      const created = await createSignedInvite();
      if (created.secret) {
        freshShareURL = inviteShareURL(created.id, created.secret);
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
    if (!invite.secret) return null;
    return inviteShareURL(invite.id, invite.secret);
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

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading invites...</h2>
        <p>Please wait while we fetch your invites.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="invites-container">
      <div class="invites-content">
        {#if canCreate}
          <button
            class="floating-create-btn"
            disabled={creating}
            on:click={createInvite}
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
                  <span class="badge" data-status={invite.status}
                    >{statusLabel(invite.status)}</span
                  >
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

      {#if showFreshLink}
        <div
          class="modal-backdrop"
          role="presentation"
          on:click={dismissFreshLink}
        >
          <div
            class="modal"
            role="dialog"
            aria-labelledby="fresh-invite-title"
            on:click|stopPropagation
          >
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
{/if}

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

  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
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

  .notice {
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    color: var(--fg);
    margin: 0 0 1rem 0;
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

  .badge[data-status='pending'] {
    color: var(--primary);
  }

  .badge[data-status='claimed'] {
    color: #27ae60;
  }

  .badge[data-status='revoked'] {
    color: var(--muted);
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
