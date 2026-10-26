<script>
  import { tick } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Avatar from '$lib/components/Avatar.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import MockSideNav from '../../MockSideNav.svelte';
  import MockToolbar from '../../MockToolbar.svelte';
  import ChatActions from '../../ChatActions.svelte';
  import { chats, me, people, patchChat, sendMessage, MAX_LENGTH } from '../../data.js';

  $: userID = $page.params.userID;
  $: person = people[userID] ?? { userID, username: userID };
  $: stored = $chats.find((c) => c.userID === userID);
  // A chat nobody has written in yet only exists once the first message is sent.
  $: chat = stored ?? { userID, unread: 0, muted: false, pinned: false, blocked: false, deleted: false, request: false, messages: [] };

  // Opening the chat reads it, unless it is still a request.
  $: if (stored && stored.unread > 0 && !stored.request) patchChat(userID, { unread: 0 });

  let draft = '';

  $: remaining = MAX_LENGTH - [...draft].length;
  $: canSend = draft.trim().length > 0 && remaining >= 0 && !chat.blocked && !chat.request;

  // An action, not bind:this: assigning scroller.scrollTop in legacy mode
  // invalidates scroller and re-runs any $: that reads it, forever.
  /** @param {HTMLElement} node @param {number} _count */
  function stickToBottom(node, _count) {
    const toEnd = () => tick().then(() => (node.scrollTop = node.scrollHeight));
    toEnd();
    return { update: toEnd };
  }

  function send() {
    if (!canSend) return;
    if (!stored) patchChat(userID, {});
    sendMessage(userID, draft.trim());
    draft = '';
  }

  /** @param {KeyboardEvent} e */
  function onKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  }

  /** @param {string} a @param {string} b */
  const sameDay = (a, b) => new Date(a).toDateString() === new Date(b).toDateString();

  /** @param {string} ts */
  const dayLabel = (ts) => new Date(ts).toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });

  /** @param {string} ts */
  const clock = (ts) => new Date(ts).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });

  const statusLabel = { sent: 'Sent', delivered: 'Delivered', read: 'Read' };
</script>

  <MockSideNav currentPage="chats" />
  <div class="mock-container">
    <div class="chat-shell">
      <header class="chat-bar">
        <a class="back" href="/mocks/chats/inbox" aria-label="Back to chats">‹</a>
        <a class="who" href="/mocks/chats/profile?u={encodeURIComponent(userID)}">
          <Avatar userID={person.userID} username={person.username} size="36px" />
          <span class="who-text">
            <span class="who-name">
              {person.username}
              {#if chat.muted}<span title="Muted" aria-label="Muted">🔕</span>{/if}
            </span>
            <span class="who-id">{person.userID}</span>
          </span>
        </a>
        <ChatActions {chat} on:deleted={() => goto('/mocks/chats/inbox')} />
      </header>

      <div class="messages" use:stickToBottom={chat.messages.length}>
        <p class="mock-note">
          Chats mockup: conversation. 140 characters per message. <a href="/mocks/chats">All mockups</a>
        </p>

        {#if chat.messages.length === 0}
          <div class="empty">
            <Avatar userID={person.userID} username={person.username} size="64px" />
            <p class="empty-title">Start a chat with {person.username}</p>
            <p class="empty-hint">Only the two of you can read it. Messages live on your devices, not on the server.</p>
          </div>
        {/if}

        {#each chat.messages as m, i (m.id)}
          {@const mine = m.from === me.userID}
          {@const grouped = i > 0 && chat.messages[i - 1].from === m.from && sameDay(chat.messages[i - 1].timestamp, m.timestamp)}
          {#if i === 0 || !sameDay(chat.messages[i - 1].timestamp, m.timestamp)}
            <div class="day">{dayLabel(m.timestamp)}</div>
          {/if}
          <div class="row" class:mine class:grouped>
            <div class="bubble" title={formatRelativeTime(m.timestamp)}>
              <span class="text">{m.content}</span>
              <span class="meta">
                {clock(m.timestamp)}
                {#if mine}
                  <span class="status {m.status}" title={statusLabel[m.status]} aria-label={statusLabel[m.status]}></span>
                {/if}
              </span>
            </div>
          </div>
        {/each}
      </div>

      {#if chat.blocked}
        <div class="footer-banner">
          You blocked {person.username}. They can't message you.
          <button class="link-btn" on:click={() => patchChat(userID, { blocked: false })}>Unblock</button>
        </div>
      {:else if chat.request}
        <div class="footer-banner request">
          <p>{person.username} isn't someone you follow. Accept to reply; they won't know you've read this until you do.</p>
          <div class="request-actions">
            <button class="btn secondary" on:click={() => patchChat(userID, { blocked: true, request: false })}>Block</button>
            <button class="btn secondary" on:click={() => { patchChat(userID, { deleted: true, request: false, messages: [] }); goto('/mocks/chats/inbox'); }}>Decline</button>
            <button class="btn primary" on:click={() => patchChat(userID, { request: false, unread: 0 })}>Accept</button>
          </div>
        </div>
      {:else}
        <form class="composer" on:submit|preventDefault={send}>
          <textarea
            bind:value={draft}
            on:keydown={onKeydown}
            rows="1"
            placeholder="Message {person.username}"
            aria-label="Message"
          ></textarea>
          <span class="counter" class:warn={remaining <= 20} class:over={remaining < 0}>{remaining}</span>
          <button class="send" type="submit" disabled={!canSend} aria-label="Send">➤</button>
        </form>
      {/if}
    </div>
    <MockToolbar currentPage="chats" />
  </div>

<style>
  .mock-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    min-height: 0;
  }

  @media (min-width: 768px) {
    .mock-container {
      padding-left: var(--sidenav-width);
    }
  }

  .chat-shell {
    flex: 1;
    display: flex;
    flex-direction: column;
    max-width: 680px;
    width: 100%;
    margin: 0 auto;
    height: calc(100dvh - 3rem - 4rem);
    box-sizing: border-box;
  }

  @media (min-width: 768px) {
    .chat-shell {
      height: calc(100dvh - 3rem - 1px);
    }
  }

  .chat-bar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.75rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .back {
    color: var(--fg);
    font-size: 1.6rem;
    line-height: 1;
    text-decoration: none;
    padding: 0 0.4rem 0.15rem;
    border-radius: 6px;
  }

  .back:hover {
    background: var(--input-bg);
  }

  .who {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    color: var(--fg);
    text-decoration: none;
  }

  .who-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .who-name {
    font-weight: 700;
    display: inline-flex;
    gap: 0.35rem;
    align-items: center;
  }

  .who-id {
    color: var(--muted);
    font-family: monospace;
    font-size: 0.72rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .messages {
    flex: 1;
    overflow-y: auto;
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }

  .mock-note {
    margin: 0 0 0.5rem;
    color: var(--muted);
    font-size: 0.8rem;
    text-align: center;
  }

  .mock-note a {
    color: var(--primary);
  }

  .empty {
    margin: auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 0.25rem;
    padding: 2rem 1rem;
  }

  .empty-title {
    margin: 0.5rem 0 0;
    font-weight: 700;
    color: var(--fg);
  }

  .empty-hint {
    margin: 0;
    max-width: 22rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .day {
    align-self: center;
    margin: 0.75rem 0 0.5rem;
    padding: 0.15rem 0.6rem;
    border-radius: 999px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.72rem;
  }

  .row {
    display: flex;
    margin-top: 0.5rem;
  }

  .row.grouped {
    margin-top: 0;
  }

  .row.mine {
    justify-content: flex-end;
  }

  .bubble {
    max-width: min(78%, 26rem);
    padding: 0.45rem 0.7rem 0.35rem;
    border-radius: 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--fg);
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 0 0.5rem;
    word-break: break-word;
  }

  .row:not(.mine):not(.grouped) .bubble {
    border-top-left-radius: 4px;
  }

  .row.mine .bubble {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--button-text);
  }

  .row.mine:not(.grouped) .bubble {
    border-top-right-radius: 4px;
  }

  .text {
    flex: 1 1 auto;
    line-height: 1.35;
  }

  .meta {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    font-size: 0.65rem;
    opacity: 0.7;
    white-space: nowrap;
  }

  .status {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    background-color: currentColor;
    -webkit-mask: url('/icons/double-tick-16.png') center / contain no-repeat;
    mask: url('/icons/double-tick-16.png') center / contain no-repeat;
  }

  .status.sent {
    width: 0.45rem;
  }

  .status.read {
    opacity: 1;
  }

  .composer {
    display: flex;
    flex-direction: row;
    align-items: flex-end;
    gap: 0.5rem;
    padding: 0.6rem 0.75rem;
    background: var(--surface);
    border-top: 1px solid var(--border);
  }

  .composer textarea {
    flex: 1;
    min-width: 0;
    margin: 0;
    resize: none;
    max-height: 6rem;
    field-sizing: content;
    padding: 0.5rem 0.75rem;
    border-radius: 18px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    font: inherit;
  }

  .counter {
    align-self: center;
    min-width: 1.8rem;
    text-align: right;
    color: var(--muted);
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
  }

  .counter.warn {
    color: var(--warning, orange);
  }

  .counter.over {
    color: var(--error);
    font-weight: 700;
  }

  .send {
    width: 2.4rem;
    height: 2.4rem;
    flex-shrink: 0;
    border-radius: 50%;
    border: none;
    background: var(--primary);
    color: var(--button-text);
    cursor: pointer;
    font-size: 1rem;
  }

  .send:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .footer-banner {
    padding: 0.75rem 1rem;
    background: var(--surface);
    border-top: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.85rem;
    text-align: center;
  }

  .footer-banner.request p {
    margin: 0 0 0.6rem;
  }

  .request-actions {
    display: flex;
    gap: 0.5rem;
    justify-content: center;
  }

  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 8px;
    font-weight: 600;
    cursor: pointer;
    width: auto;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
    border: none;
  }

  .btn.secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .link-btn {
    background: none;
    border: none;
    padding: 0;
    margin-left: 0.4rem;
    color: var(--primary);
    font: inherit;
    cursor: pointer;
    width: auto;
  }
</style>
