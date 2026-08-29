<script lang="ts">
  import { goto } from '$app/navigation';
  import { mailboxRepository, type MailboxRecord, type MailboxCategory } from '$lib/repositories/mailbox';
  import { mailboxMessages, mailboxUnread, refreshMailboxMessages } from '$lib/stores/mailbox';
  import { formatRelativeTime } from '$lib/utils/time';
  import Avatar from './Avatar.svelte';

  let open = false;
  let container: HTMLDivElement;
  let activeTab: MailboxCategory = 'interaction';
  let unreadInteractionCount = 0;
  let unreadSystemCount = 0;

  // $mailboxMessages is the live IndexedDB mirror, refreshed on every WS
  // receipt (see +layout.svelte) — reading it directly here means the
  // popover updates while open, with no reload-on-toggle needed.
  $: visibleMessages = $mailboxMessages.filter((m) => m.category === activeTab);
  $: unreadCount = visibleMessages.filter((m) => !m.isRead).length;

  // Re-selects the tab with unread content whenever the *set* of unread
  // messages grows (new arrival, catch-up) — but not on every store
  // update, so marking a message read while browsing the other tab
  // doesn't yank the user back. Ties/none favor Interactions.
  $: {
    const nextInteraction = $mailboxMessages.filter((m) => m.category === 'interaction' && !m.isRead).length;
    const nextSystem = $mailboxMessages.filter((m) => m.category === 'system' && !m.isRead).length;
    if (nextInteraction > unreadInteractionCount || nextSystem > unreadSystemCount) {
      activeTab =
        nextSystem > unreadSystemCount && nextInteraction <= unreadInteractionCount
          ? 'system'
          : 'interaction';
    }
    unreadInteractionCount = nextInteraction;
    unreadSystemCount = nextSystem;
  }

  function toggle() {
    open = !open;
  }

  function close() {
    open = false;
  }

  function handleWindowClick(event: MouseEvent) {
    if (open && container && !container.contains(event.target as Node)) {
      close();
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') close();
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

  async function deleteMessage(message: MailboxRecord) {
    await mailboxRepository.delete(message.id);
    await refreshMailboxMessages();
  }

  async function markAllRead() {
    const ids = visibleMessages.filter((m) => !m.isRead).map((m) => m.id);
    await Promise.all(ids.map((id) => mailboxRepository.markRead(id)));
    await refreshMailboxMessages();
  }
</script>

<svelte:window on:click={handleWindowClick} on:keydown={handleKeydown} />

<div class="mailbox-bell" bind:this={container} on:click|stopPropagation role="presentation">
  <button
    class="bell-trigger"
    class:active={open}
    on:click|stopPropagation={toggle}
    aria-label="Mailbox"
    aria-haspopup="true"
    aria-expanded={open}
  >
    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 2a6 6 0 0 0-6 6v3.2c0 .6-.2 1.2-.6 1.7L4 15.5c-.6.8 0 2 1 2h14c1 0 1.6-1.2 1-2l-1.4-2.6c-.4-.5-.6-1.1-.6-1.7V8a6 6 0 0 0-6-6z"/>
      <path d="M9.5 19a2.5 2.5 0 0 0 5 0z"/>
    </svg>
    {#if $mailboxUnread}
      <span class="unread-dot" aria-hidden="true"></span>
    {/if}
  </button>

  {#if open}
    <div class="mailbox-popover">
      <div class="mailbox-tabs" role="tablist">
        <button
          class="mailbox-tab"
          class:active={activeTab === 'interaction'}
          role="tab"
          aria-selected={activeTab === 'interaction'}
          on:click|stopPropagation={() => (activeTab = 'interaction')}
        >
          Interactions
          {#if unreadInteractionCount > 0}
            <span class="tab-dot" aria-hidden="true"></span>
          {/if}
        </button>
        <button
          class="mailbox-tab"
          class:active={activeTab === 'system'}
          role="tab"
          aria-selected={activeTab === 'system'}
          on:click|stopPropagation={() => (activeTab = 'system')}
        >
          System
          {#if unreadSystemCount > 0}
            <span class="tab-dot" aria-hidden="true"></span>
          {/if}
        </button>
      </div>

      <div class="mailbox-list" role="menu">
        {#if visibleMessages.length === 0}
          <div class="mailbox-empty">
            {activeTab === 'interaction' ? 'No interactions yet' : 'No system messages'}
          </div>
        {:else}
          {#each visibleMessages as message (message.id)}
            <div
              class="mailbox-row"
              class:unread={!message.isRead}
              role="menuitem"
              on:click={() => openMessage(message)}
              on:keydown={(e) => e.key === 'Enter' && openMessage(message)}
              tabindex="0"
            >
              <Avatar userID={message.senderUserID} size="2rem" />
              <div class="mailbox-content">
                <span class="mailbox-message">{message.message}</span>
                <span class="mailbox-time">{formatRelativeTime(message.createdAt)}</span>
              </div>
              <div class="mailbox-actions">
                {#if !message.isRead}
                  <button
                    class="mailbox-action"
                    aria-label="Mark as read"
                    title="Mark as read"
                    on:click|stopPropagation={() => markRead(message)}
                  >
                    <span class="mailbox-action-icon" style="-webkit-mask-image: url('/icons/double-tick-16.png'); mask-image: url('/icons/double-tick-16.png');"></span>
                  </button>
                {/if}
                <button
                  class="mailbox-action danger"
                  aria-label="Delete"
                  title="Delete"
                  on:click|stopPropagation={() => deleteMessage(message)}
                >
                  <span class="mailbox-action-icon" style="-webkit-mask-image: url('/icons/trash-16.png'); mask-image: url('/icons/trash-16.png');"></span>
                </button>
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

<style>
  /* Absolutely positioned within <header> (position:relative, see
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
    width: 22rem;
    max-height: 26rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    overflow: hidden;
  }

  .mailbox-tabs {
    display: flex;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .mailbox-tab {
    position: relative;
    flex: 1;
    background: none;
    border: none;
    padding: 0.6rem 0.5rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--muted);
    cursor: pointer;
    border-bottom: 2px solid transparent;
  }

  .mailbox-tab:hover {
    color: var(--fg);
  }

  .mailbox-tab.active {
    color: var(--fg);
    border-bottom-color: var(--primary);
  }

  .tab-dot {
    display: inline-block;
    width: 0.4rem;
    height: 0.4rem;
    margin-left: 0.35rem;
    border-radius: 50%;
    background: var(--error);
    vertical-align: middle;
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
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 0.5rem;
    border-radius: 4px;
    cursor: pointer;
  }

  .mailbox-row:hover,
  .mailbox-row.unread {
    background: var(--input-bg);
  }

  .mailbox-content {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
    flex: 1;
  }

  .mailbox-message {
    font-size: 0.9rem;
    color: var(--fg);
    white-space: normal;
    text-align: left;
  }

  .mailbox-time {
    font-size: 0.75rem;
    color: var(--muted);
  }

  .mailbox-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .mailbox-action {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--muted);
    border-radius: 4px;
  }

  .mailbox-action:hover {
    color: var(--fg);
    background: var(--surface);
  }

  .mailbox-action.danger:hover {
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
