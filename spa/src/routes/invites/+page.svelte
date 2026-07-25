<script lang="ts">
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
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

  type InviteRow = {
    id: string;
    createdAt: string;
    status: 'pending' | 'claimed' | 'revoked';
    claimedAt: string | null;
    claimedBy: { id: string; username: string } | null;
    revokedAt: string | null;
  };

  let user = null;
  let loading = true;
  let invites: InviteRow[] = [];
  let creating = false;
  let revokingId: string | null = null;
  let freshShareURL = '';
  let showFreshLink = false;

  $: maxInvites = $serverInfo?.maxInvitesPerUser ?? -1;
  $: atQuota = maxInvites !== -1 && invites.length >= maxInvites;
  $: canCreate = !$isSignupClosed && !atQuota;
  $: quotaLabel =
    maxInvites === -1 ? '' : `${invites.length} / ${maxInvites}`;

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
      const res = await apiService.listInvites();
      invites = res.invites ?? [];
    } catch (err) {
      console.error(err);
      notificationStore.error(
        err instanceof Error ? err.message : 'Failed to load invites'
      );
    }
  }

  async function createInvite() {
    if (!canCreate || creating) return;
    creating = true;
    try {
      const created = await apiService.createInvite();
      freshShareURL = `${window.location.origin}/signup?invite=${created.token}`;
      showFreshLink = true;
      await loadInvites();
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
    revokingId = id;
    try {
      await apiService.revokeInvite(id);
      await loadInvites();
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

  async function copyFreshLink() {
    try {
      await navigator.clipboard.writeText(freshShareURL);
      notificationStore.success('Invite link copied');
    } catch (err) {
      console.error(err);
      notificationStore.error('Failed to copy invite link');
    }
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
  <div class="page">
    <div class="container">
      <header class="header">
        <h1>Invites</h1>
        {#if quotaLabel}
          <p class="quota">{quotaLabel} used</p>
        {/if}
      </header>

      {#if loading}
        <p class="muted">Loading…</p>
      {:else}
        {#if $isSignupClosed}
          <p class="notice">
            Signups are closed on this server — you can’t create new invites.
          </p>
        {:else if atQuota}
          <p class="notice">
            You’ve reached the invite limit ({maxInvites}). Revoking unused
            invites does not free quota.
          </p>
        {/if}

        <div class="actions">
          <button
            class="btn primary"
            disabled={!canCreate || creating}
            on:click={createInvite}
          >
            {creating ? 'Creating…' : 'Create invite'}
          </button>
        </div>

        {#if invites.length === 0}
          <p class="muted empty">
            {#if canCreate}
              No invites yet. Create one to share a signup link.
            {:else if $isSignupClosed}
              No invites to show.
            {:else}
              No invites yet.
            {/if}
          </p>
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
                {#if invite.status === 'pending'}
                  <button
                    class="btn danger"
                    disabled={revokingId === invite.id}
                    on:click={() => revokeInvite(invite.id)}
                  >
                    {revokingId === invite.id ? 'Revoking…' : 'Revoke'}
                  </button>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>

    {#if showFreshLink}
      <div class="modal-backdrop" role="presentation" on:click={dismissFreshLink}>
        <div
          class="modal"
          role="dialog"
          aria-labelledby="fresh-invite-title"
          on:click|stopPropagation
        >
          <h2 id="fresh-invite-title">Invite link ready</h2>
          <p class="notice">
            Copy this link now — you won’t be able to see it again.
          </p>
          <div class="link-row">
            <code class="share-url">{freshShareURL}</code>
            <CopyButton ariaLabel="Copy invite link" on:click={copyFreshLink} />
          </div>
          <button class="btn primary" on:click={dismissFreshLink}>Done</button>
        </div>
      </div>
    {/if}

    <BottomToolbar currentPage="invites" />
  </div>
</Auth>

<style>
  .page {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }

  .container {
    flex: 1;
    max-width: 640px;
    width: 100%;
    margin: 0 auto;
    padding: 1.5rem 1rem 2rem;
  }

  .header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
  }

  .header h1 {
    margin: 0;
    font-size: 1.5rem;
  }

  .quota {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .muted {
    color: var(--muted);
  }

  .empty {
    margin-top: 1.5rem;
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

  .actions {
    margin-bottom: 1.25rem;
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
    border-radius: 8px;
    background: var(--surface);
  }

  .invite-main {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
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
</style>
