<script lang="ts">
  import { goto } from '$app/navigation';
  import { mailboxRepository, type MailboxRecord } from '$lib/repositories/mailbox';
  import { mailboxMessages, mailboxUnread, refreshMailboxMessages } from '$lib/stores/mailbox';
  import { formatRelativeTime } from '$lib/utils/time';
  import ConfirmDialog from './ConfirmDialog.svelte';

  let open = false;
  let container: HTMLDivElement;
  let deleteTarget: MailboxRecord | null = null;

  // $mailboxMessages is the live IndexedDB mirror, refreshed on every WS
  // receipt (see +layout.svelte), so the popover updates while open.
  $: unreadCount = $mailboxMessages.filter((m) => !m.isRead).length;

  const kindLabels: Record<string, string> = {
    broadcast: 'Broadcast',
    'admin-message': 'Message from @admins',
  };
  const kindIcons: Record<string, string> = {
    broadcast: '/icons/megaphone-16.svg',
    'admin-message': '/icons/admin-16.svg',
  };

  function toggle() {
    open = !open;
  }

  function close() {
    open = false;
  }

  function handleWindowClick(event: MouseEvent) {
    // The delete confirmation renders outside the container.
    if (deleteTarget) return;
    if (open && container && !container.contains(event.target as Node)) {
      close();
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    if (deleteTarget) deleteTarget = null;
    else close();
  }

  async function openMessage(message: MailboxRecord) {
    await markRead(message);
    if (message.link) {
      close();
      goto(message.link);
    }
  }

  async function markRead(message: MailboxRecord) {
    if (message.isRead) return;
    await mailboxRepository.markRead(message.id);
    await refreshMailboxMessages();
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    const message = deleteTarget;
    deleteTarget = null;
    await mailboxRepository.delete(message.id);
    await refreshMailboxMessages();
  }

  async function markAllRead() {
    await mailboxRepository.markAllRead();
    await refreshMailboxMessages();
  }

  function maskStyle(url: string): string {
    return `-webkit-mask-image: url('${url}'); mask-image: url('${url}');`;
  }
</script>

<svelte:window on:click={handleWindowClick} on:keydown={handleKeydown} />

<div class="mailbox-bell" bind:this={container} on:click|stopPropagation role="presentation">
  <button
    class="bell-trigger"
    class:active={open}
    on:click|stopPropagation={toggle}
    aria-label={open ? 'Close mailbox' : 'Mailbox'}
    aria-haspopup="true"
    aria-expanded={open}
  >
    {#if open}
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 -960 960 960" fill="currentColor">
        <path d="m256-200-56-56 224-224-224-224 56-56 224 224 224-224 56 56-224 224 224 224-56 56-224-224-224 224Z"/>
      </svg>
    {:else}
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
        <path d="M12 2a6 6 0 0 0-6 6v3.2c0 .6-.2 1.2-.6 1.7L4 15.5c-.6.8 0 2 1 2h14c1 0 1.6-1.2 1-2l-1.4-2.6c-.4-.5-.6-1.1-.6-1.7V8a6 6 0 0 0-6-6z"/>
        <path d="M9.5 19a2.5 2.5 0 0 0 5 0z"/>
      </svg>
      {#if $mailboxUnread}
        <span class="unread-dot" aria-hidden="true"></span>
      {/if}
    {/if}
  </button>

  {#if open}
    <div class="mailbox-popover">
      <div class="mailbox-list" role="menu">
        {#if $mailboxMessages.length === 0}
          <div class="mailbox-empty">No messages</div>
        {:else}
          {#each $mailboxMessages as message (message.id)}
            <div
              class="mailbox-row"
              class:unread={!message.isRead}
              role="menuitem"
              on:click={() => openMessage(message)}
              on:keydown={(e) => e.key === 'Enter' && openMessage(message)}
              tabindex="0"
            >
              <span class="mailbox-system" aria-hidden="true">
                <span class="mailbox-system-icon" style={maskStyle(kindIcons[message.kind] ?? '/icons/info-16.svg')}></span>
              </span>
              <div class="mailbox-content">
                <span class="mailbox-from">
                  {#if message.isRead}
                    <span class="mailbox-envelope read" title="Read">
                      <span class="mailbox-action-icon" style={maskStyle('/icons/drafts-16.svg')}></span>
                    </span>
                  {:else}
                    <button
                      class="mailbox-envelope"
                      aria-label="Mark as read"
                      title="Mark as read"
                      on:click|stopPropagation={() => markRead(message)}
                    >
                      <span class="mailbox-action-icon" style={maskStyle('/icons/mail-16.svg')}></span>
                    </button>
                  {/if}
                  {kindLabels[message.kind] ?? 'System'}
                </span>
                <p class="mailbox-message">{message.message}</p>
                <p class="mailbox-time">
                  {formatRelativeTime(message.createdAt)} ·
                  <button class="mailbox-delete" on:click|stopPropagation={() => (deleteTarget = message)}>Delete</button>
                </p>
              </div>
            </div>
          {/each}
          {#if unreadCount >= 2}
            <button class="mailbox-mark-all" on:click|stopPropagation={markAllRead}>
              Mark all as read
            </button>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</div>

{#if deleteTarget}
  <ConfirmDialog
    title="Delete message?"
    message="This message will be removed from your mailbox. This can't be undone."
    on:confirm={confirmDelete}
    on:cancel={() => (deleteTarget = null)}
  />
{/if}

<style>
  /* Absolutely positioned within <header> (position:sticky, see
     styles.css) so the bell sits right-aligned at the logo's level
     without disturbing the logo's own centering. */
  .mailbox-bell {
    position: absolute;
    top: 50%;
    right: 1rem;
    transform: translateY(-50%);
  }

  .bell-trigger {
    position: relative;
    background: transparent;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--muted);
    border-radius: 4px;
    width: auto;
  }

  .bell-trigger:hover,
  .bell-trigger.active {
    color: var(--fg);
    background: var(--input-bg);
  }

  .unread-dot {
    position: absolute;
    top: 0.1rem;
    right: 0.1rem;
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--error);
  }

  .mailbox-popover {
    position: absolute;
    top: calc(100% + 0.25rem);
    right: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    width: min(22rem, calc(100vw - 2rem));
    max-height: 26rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    overflow: hidden;
  }

  .mailbox-list {
    display: flex;
    flex-direction: column;
    padding: 0.25rem;
    overflow-y: auto;
  }

  .mailbox-empty {
    padding: 1.5rem 0.75rem;
    font-size: 0.9rem;
    color: var(--muted);
    text-align: center;
  }

  .mailbox-row {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    padding: 0.5rem;
    border-radius: 4px;
    cursor: pointer;
    text-align: left;
  }

  .mailbox-row:hover,
  .mailbox-row.unread {
    background: var(--input-bg);
  }

  .mailbox-system {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--primary);
  }

  .mailbox-system-icon {
    display: inline-block;
    width: 1rem;
    height: 1rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .mailbox-content {
    flex: 1;
    min-width: 0;
  }

  .mailbox-from {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.75rem;
    line-height: 1rem;
    color: var(--muted);
  }

  .mailbox-message {
    margin: 0;
    font-size: 0.9rem;
    line-height: 1.25rem;
    color: var(--fg);
    overflow-wrap: anywhere;
  }

  .mailbox-time {
    margin: 0.25rem 0 0;
    font-size: 0.75rem;
    color: var(--muted);
  }

  .mailbox-envelope {
    display: inline-flex;
    align-items: center;
    background: none;
    border: none;
    padding: 0;
    width: auto;
    color: var(--muted);
    cursor: pointer;
  }

  .mailbox-envelope:hover {
    color: var(--fg);
  }

  .mailbox-envelope.read {
    cursor: default;
    opacity: 0.6;
  }

  .mailbox-envelope.read:hover {
    color: var(--muted);
  }

  .mailbox-delete {
    background: none;
    border: none;
    padding: 0;
    width: auto;
    font: inherit;
    color: var(--muted);
    text-decoration: underline;
    cursor: pointer;
  }

  .mailbox-delete:hover {
    color: var(--error);
  }

  .mailbox-mark-all {
    background: none;
    border: none;
    border-top: 1px solid var(--border);
    margin-top: 0.25rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    color: var(--muted);
    cursor: pointer;
    text-align: center;
  }

  .mailbox-mark-all:hover {
    color: var(--fg);
  }

  .mailbox-action-icon {
    display: inline-block;
    width: 0.9rem;
    height: 0.9rem;
    flex-shrink: 0;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }
</style>
