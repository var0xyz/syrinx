<script lang="ts">
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import { formatRelativeTime } from '$lib/utils/time';

  // Mockup: sending only adds to the local list below, nothing reaches the server.
  interface SentRequest {
    id: string;
    recipient: string | null;
    message: string;
    createdAt: Date;
  }

  const MAX_LENGTH = 1000;

  const tabs = [
    { href: '/admin/actions', label: 'Actions' },
    { href: '/admin/broadcast', label: 'Broadcast' },
    { href: '/admin/mesh', label: 'Mesh' },
  ];

  const recipientOptions = [
    { value: 'everyone', label: 'Everyone' },
    { value: 'user', label: 'One user' },
  ];

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';

  let recipientMode: 'everyone' | 'user' = 'everyone';
  let username = '';
  let message = '';
  let sent: SentRequest[] = [
    {
      id: 's1',
      recipient: null,
      message: 'The server will be down for maintenance on Saturday from 02:00 to 04:00 UTC.',
      createdAt: new Date(Date.now() - 3 * 60 * 60_000),
    },
  ];

  $: canSend =
    message.trim().length > 0 && (recipientMode === 'everyone' || username.trim().length > 0);

  function send() {
    if (!canSend) return;
    sent = [
      {
        id: crypto.randomUUID(),
        recipient: recipientMode === 'user' ? username.trim() : null,
        message: message.trim(),
        createdAt: new Date(),
      },
      ...sent,
    ];
    message = '';
    username = '';
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
        <p class="mock-banner">Mockup: sample data, nothing is sent to the server.</p>

        <form class="composer" on:submit|preventDefault={send}>
          <div class="recipient-picker">
            <ModeSwitch options={recipientOptions} bind:value={recipientMode} />
          </div>

          {#if recipientMode === 'user'}
            <label class="field">
              <span>To</span>
              <input type="text" bind:value={username} placeholder="Username" autocomplete="off" spellcheck="false" />
            </label>
          {/if}

          <label class="field">
            <span>Message</span>
            <textarea bind:value={message} rows="6" maxlength={MAX_LENGTH}></textarea>
          </label>

          <div class="composer-footer">
            <span class="char-count">{message.length}/{MAX_LENGTH}</span>
            <button class="btn primary" type="submit" disabled={!canSend}>Send for approval</button>
          </div>
        </form>

        {#if sent.length > 0}
          <h2 class="section-title">Awaiting approval</h2>
          <ul class="sent-list">
            {#each sent as s (s.id)}
              <li class="sent">
                <div class="sent-head">
                  <span class="recipient" class:name={!!s.recipient}>{s.recipient ?? 'Everyone'}</span>
                  {#if s.recipient}<span class="badge">Private</span>{/if}
                  <span class="when">{formatRelativeTime(s.createdAt)}</span>
                </div>
                <blockquote class="message">{s.message}</blockquote>
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

  .mock-banner {
    margin: 0 0 1rem;
    padding: 0.5rem 0.75rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.8rem;
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

  .field input,
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
