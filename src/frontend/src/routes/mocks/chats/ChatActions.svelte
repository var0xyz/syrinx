<script>
  import { createEventDispatcher } from 'svelte';
  import { goto } from '$app/navigation';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { people, patchChat } from './data.js';

  /** The same menu in the chat list and the chat's top bar. */
  export let chat;

  const dispatch = createEventDispatcher();

  /** @type {null | 'delete' | 'block' | 'report'} */
  let confirming = null;

  $: person = people[chat.userID];
  $: name = person?.username ?? chat.userID;

  $: options = [
    { label: chat.muted ? 'Unmute' : 'Mute', onSelect: () => patchChat(chat.userID, { muted: !chat.muted }) },
    { label: chat.pinned ? 'Unpin' : 'Pin to top', icon: '/icons/pin-16-outlined.png', onSelect: () => patchChat(chat.userID, { pinned: !chat.pinned }) },
    chat.unread > 0
      ? { label: 'Mark as read', icon: '/icons/double-tick-16.png', onSelect: () => patchChat(chat.userID, { unread: 0 }) }
      : { label: 'Mark as unread', onSelect: () => patchChat(chat.userID, { unread: 1 }) },
    { label: 'View profile', icon: '/icons/user-16.svg', onSelect: () => goto(`/mocks/chats/profile?u=${encodeURIComponent(chat.userID)}`) },
    { label: 'Report', danger: true, onSelect: () => (confirming = 'report') },
    chat.blocked
      ? { label: `Unblock ${name}`, onSelect: () => patchChat(chat.userID, { blocked: false }) }
      : { label: `Block ${name}`, danger: true, onSelect: () => (confirming = 'block') },
    { label: 'Delete chat', danger: true, icon: '/icons/trash-16.png', onSelect: () => (confirming = 'delete') },
  ];

  function confirm() {
    if (confirming === 'delete') {
      patchChat(chat.userID, { deleted: true, messages: [], unread: 0, pinned: false });
      dispatch('deleted');
    } else if (confirming === 'block') {
      patchChat(chat.userID, { blocked: true, unread: 0, request: false });
    }
    confirming = null;
  }
</script>

<KebabMenu {options} ariaLabel="Chat options" />

{#if confirming === 'delete'}
  <ConfirmDialog
    title="Delete this chat?"
    message="Removes the conversation from this device. {name} keeps their copy; a new message from them starts a fresh chat."
    confirmLabel="Delete"
    on:confirm={confirm}
    on:cancel={() => (confirming = null)}
  />
{:else if confirming === 'block'}
  <ConfirmDialog
    title="Block {name}?"
    message="They can no longer message you, and their messages are dropped before they reach this device. They are not told."
    confirmLabel="Block"
    on:confirm={confirm}
    on:cancel={() => (confirming = null)}
  />
{:else if confirming === 'report'}
  <ConfirmDialog
    title="Report {name}?"
    message="Sends the last few messages you choose, with their signatures, to your server's admins. The rest of the chat stays private."
    confirmLabel="Report"
    on:confirm={confirm}
    on:cancel={() => (confirming = null)}
  />
{/if}
