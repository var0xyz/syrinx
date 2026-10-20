<script lang="ts">
  import { countCharacters } from '$lib/utils/text';
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import AdminRequestList from '$lib/components/AdminRequestList.svelte';
  import UserPicker from '$lib/components/UserPicker.svelte';
  import { apiService } from '$lib/services/api';
  import { isMailRequest, openAdminRequest } from '$lib/services/admin';
  import { keepInHash } from '$lib/utils/hashMode';
  import { adminPermissions, adminTabs, canMail, holds } from '$lib/stores/adminPermissions';

  const MAX_LENGTH = 140;

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';

  let recipientMode: 'broadcast' | 'unicast' = 'broadcast';
  let username = '';
  let recipientID = '';
  let message = '';
  let sending = false;
  let error = '';

  const stateOptions = [
    { value: 'open', label: 'Open' },
    { value: 'closed', label: 'Closed' },
  ];
  let state: 'open' | 'closed' = $page.url.hash === '#closed' ? 'closed' : 'open';
  $: keepInHash($page.url, state === 'closed' ? '#closed' : '');

  // Permissions only pick which options show; the server checks the send.
  $: canBroadcast = holds($page.data?.user, $adminPermissions, 'message.broadcast');
  $: canUnicast = holds($page.data?.user, $adminPermissions, 'message.unicast');
  $: recipientOptions = [
    ...(canBroadcast || !canUnicast ? [{ value: 'broadcast', label: 'Broadcast' }] : []),
    ...(canUnicast || !canBroadcast ? [{ value: 'unicast', label: 'Unicast' }] : []),
  ];
  $: if (!canBroadcast && canUnicast) recipientMode = 'unicast';
  $: canSend =
    !sending &&
    message.trim().length > 0 &&
    countCharacters(message) <= MAX_LENGTH &&
    (recipientMode === 'broadcast' || username.trim().length > 0);

  /** The local member named name, case-insensitively; never root. */
  async function recipient(name: string): Promise<string | null> {
    const wanted = name.trim().replace(/^@/, '').toLowerCase();
    const { users } = await apiService.searchUsers(wanted, 20, undefined, { local: true, excludeRoot: true });
    const match = users.find((u) => u.username.toLowerCase() === wanted);
    return match?.id ?? null;
  }

  async function send(): Promise<void> {
    if (!canSend) return;
    sending = true;
    error = '';
    try {
      if (recipientMode === 'broadcast') {
        await openAdminRequest('broadcast', [], '', message.trim());
      } else {
        const to = recipientID || (await recipient(username));
        if (!to) {
          error = `No member here is named ${username.trim()}.`;
          return;
        }
        await openAdminRequest('unicast', [to], '', message.trim());
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
    <SectionTabs {tabs} active="message" />
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

          {#if recipientMode === 'unicast'}
            <label class="field">
              <span>To</span>
              <UserPicker bind:value={username} bind:userID={recipientID} local excludeRoot />
            </label>
          {/if}

          <label class="field">
            <span>Message</span>
            <textarea bind:value={message} rows="4"></textarea>
          </label>

          {#if error}<p class="error" role="alert">{error}</p>{/if}

          <div class="composer-footer">
            <span class="char-count">{countCharacters(message)}/{MAX_LENGTH}</span>
            <button class="btn primary" type="submit" disabled={!canSend}>
              {sending ? 'Sending…' : 'Send for approval'}
            </button>
          </div>
        </form>

        <div class="requests">
          <div class="state-picker">
            <ModeSwitch options={stateOptions} bind:value={state} />
          </div>
          <AdminRequestList
            include={isMailRequest}
            {state}
            emptyTitle={state === 'open' ? 'No messages awaiting approval' : 'No closed messages'}
          />
        </div>
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

  .requests {
    margin-top: 1.5rem;
  }

  .state-picker {
    display: flex;
    justify-content: center;
    margin-bottom: 1rem;
  }

  .composer .error {
    margin: 0;
    font-size: 0.85rem;
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
