<script lang="ts">
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import Username from '$lib/components/Username.svelte';
  import UserPicker from '$lib/components/UserPicker.svelte';
  import { apiService } from '$lib/services/api';
  import { adminRequestsChanged, openAdminRequest } from '$lib/services/admin';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';
  import { adminPermissions, adminTabs, canMail, holds } from '$lib/stores/adminPermissions';
  import type * as api from '$lib/types/api';

  const MAX_LENGTH = 140;

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: viewerID = $page.data?.user?.id ?? '';
  $: serverID = parseCanonicalId(viewerID)?.[1] ?? '';

  let recipientMode: 'everyone' | 'user' = 'everyone';
  let username = '';
  let recipientID = '';
  let message = '';
  let sending = false;
  let error = '';
  let sent: api.AdminRequest[] = [];

  // Permissions only pick which options show; the server checks the send.
  $: canBroadcast = holds($page.data?.user, $adminPermissions, 'broadcast.message');
  $: canMessage = holds($page.data?.user, $adminPermissions, 'user.message');
  $: recipientOptions = [
    ...(canBroadcast || !canMessage ? [{ value: 'everyone', label: 'Everyone' }] : []),
    ...(canMessage || !canBroadcast ? [{ value: 'user', label: 'One user' }] : []),
  ];
  $: if (!canBroadcast && canMessage) recipientMode = 'user';
  $: canSend =
    !sending &&
    message.trim().length > 0 &&
    (recipientMode === 'everyone' || username.trim().length > 0);

  $: $adminRequestsChanged, isAdmin && void loadSent();

  async function loadSent(): Promise<void> {
    const open = await apiService.listAdminRequests('pending');
    sent = open.requests.filter(
      (r) => r.requesterId === viewerID && (r.action === 'broadcast' || r.action === 'message')
    );
  }

  /** The local member named name, case-insensitively. */
  async function recipient(name: string): Promise<string | null> {
    const wanted = name.trim().replace(/^@/, '').toLowerCase();
    const { users } = await apiService.searchUsers(wanted, 20);
    const match = users.find(
      (u) => u.username.toLowerCase() === wanted && parseCanonicalId(u.id)?.[1] === serverID
    );
    return match?.id ?? null;
  }

  async function send(): Promise<void> {
    if (!canSend) return;
    sending = true;
    error = '';
    try {
      if (recipientMode === 'everyone') {
        await openAdminRequest('broadcast', [], '', message.trim());
      } else {
        const to = recipientID || (await recipient(username));
        if (!to) {
          error = `No member here is named ${username.trim()}.`;
          return;
        }
        await openAdminRequest('message', [to], '', message.trim());
      }
      message = '';
      username = '';
      recipientID = '';
    } catch (err) {
      error = err instanceof Error ? err.message : 'The request was refused.';
    } finally {
      sending = false;
    }
  }
</script>

<Auth>
  <SideNav currentPage="admin" />
  <div class="admin-container">
    <SectionTabs {tabs} active="broadcast" />
    <div class="admin-content">
      {#if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        <form class="composer" on:submit|preventDefault={send}>
          {#if recipientOptions.length > 1}
            <div class="recipient-picker">
              <ModeSwitch options={recipientOptions} bind:value={recipientMode} />
            </div>
          {/if}

          {#if recipientMode === 'user'}
            <label class="field">
              <span>To</span>
              <UserPicker bind:value={username} bind:userID={recipientID} {serverID} />
            </label>
          {/if}

          <label class="field">
            <span>Message</span>
            <textarea bind:value={message} rows="4" maxlength={MAX_LENGTH}></textarea>
          </label>

          {#if error}<p class="error" role="alert">{error}</p>{/if}

          <div class="composer-footer">
            <span class="char-count">{message.length}/{MAX_LENGTH}</span>
            <button class="btn primary" type="submit" disabled={!canSend}>
              {sending ? 'Signing…' : 'Send for approval'}
            </button>
          </div>
        </form>

        {#if sent.length > 0}
          <h2 class="section-title">Awaiting approval</h2>
          <ul class="sent-list">
            {#each sent as s (s.id)}
              <li class="sent">
                <div class="sent-head">
                  {#if s.action === 'message'}
                    <span class="recipient name"><Username userID={s.targets[0]} color="var(--primary)" /></span>
                    <span class="badge">Private</span>
                  {:else}
                    <span class="recipient">Everyone</span>
                  {/if}
                  <span class="when">{formatRelativeTime(fromUnix(s.createdAt) ?? new Date())}</span>
                </div>
                <blockquote class="message">{s.body}</blockquote>
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>
</Auth>

<style>
  .admin-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .admin-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .admin-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .admin-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .composer {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .recipient-picker {
    display: flex;
    justify-content: center;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .field :global(input),
  .field textarea {
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.9rem;
  }

  .field textarea {
    resize: vertical;
  }

  .composer-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .char-count {
    font-size: 0.8rem;
    color: var(--muted);
  }

  .section-title {
    margin: 1.5rem 0 0.75rem;
    color: var(--muted);
    font-size: 0.85rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .sent-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .sent {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  .sent-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .recipient {
    font-weight: 600;
    color: var(--fg);
  }

  .recipient.name {
    color: var(--primary);
  }

  .composer .error {
    margin: 0;
    font-size: 0.85rem;
  }

  .badge {
    font-size: 0.75rem;
    font-weight: 600;
    padding: 0.25rem 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: var(--muted);
  }

  .when {
    margin-left: auto;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .message {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .btn {
    width: auto;
    border: none;
    border-radius: 8px;
    padding: 0.5rem 1rem;
    font-family: inherit;
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

  .error {
    color: #b91c1c;
  }
</style>
