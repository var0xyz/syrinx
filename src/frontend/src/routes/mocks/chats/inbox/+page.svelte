<script>
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import ChatActions from '../ChatActions.svelte';
  import { chats, me, people } from '../data.js';

  const last = (c) => c.messages[c.messages.length - 1];

  $: visible = $chats
    .filter((c) => !c.deleted && c.messages.length > 0)
    .sort((a, b) => Number(b.pinned) - Number(a.pinned) || last(b).timestamp.localeCompare(last(a).timestamp));
  $: requests = visible.filter((c) => c.request && !c.blocked);
  $: conversations = visible.filter((c) => !c.request);

  /** @param {string} userID */
  function open(userID) {
    void goto(`/mocks/chats/with/${userID}`);
  }
</script>

<Auth>
  <MockSideNav currentPage="chats" />
  <div class="mock-container">
    <div class="mock-content">
      <p class="mock-note">
        Chats mockup: list. Pinned first, then newest. Every row has the same menu as the chat's top bar.
        <a href="/mocks/chats">All mockups</a>
      </p>

      <h1 class="page-title">Chats</h1>

      {#if requests.length > 0}
        <h2 class="section-title">Requests <span class="count">{requests.length}</span></h2>
        <p class="section-hint">From people you don't follow. They can't see if you've read it until you accept.</p>
        <ul class="chat-list">
          {#each requests as chat (chat.userID)}
            {@const person = people[chat.userID]}
            <li class="chat-row request" role="button" tabindex="0" on:click={() => open(chat.userID)} on:keydown={(e) => e.key === 'Enter' && open(chat.userID)}>
              <Avatar userID={chat.userID} username={person?.username} size="44px" />
              <div class="chat-main">
                <div class="chat-top">
                  <span class="chat-name">{person?.username ?? chat.userID}</span>
                  <span class="chat-time">{formatRelativeTime(last(chat).timestamp)}</span>
                </div>
                <div class="chat-preview">{last(chat).content}</div>
              </div>
              <ChatActions {chat} />
            </li>
          {/each}
        </ul>
      {/if}

      {#if conversations.length === 0}
        <div class="empty">
          <p>No chats yet.</p>
          <p class="section-hint">Open someone's profile and tap <strong>Chat</strong> to start one.</p>
        </div>
      {:else}
        <ul class="chat-list">
          {#each conversations as chat (chat.userID)}
            {@const person = people[chat.userID]}
            {@const m = last(chat)}
            <li
              class="chat-row"
              class:unread={chat.unread > 0}
              class:blocked={chat.blocked}
              role="button"
              tabindex="0"
              on:click={() => open(chat.userID)}
              on:keydown={(e) => e.key === 'Enter' && open(chat.userID)}
            >
              <Avatar userID={chat.userID} username={person?.username} size="44px" />
              <div class="chat-main">
                <div class="chat-top">
                  <span class="chat-name">
                    {person?.username ?? chat.userID}
                    {#if chat.pinned}<span class="flag pin" title="Pinned" aria-label="Pinned"></span>{/if}
                    {#if chat.muted}<span class="flag" title="Muted" aria-label="Muted">🔕</span>{/if}
                    {#if chat.blocked}<span class="badge">Blocked</span>{/if}
                  </span>
                  <span class="chat-time">{formatRelativeTime(m.timestamp)}</span>
                </div>
                <div class="chat-bottom">
                  <span class="chat-preview">
                    {#if m.from === me.userID}<span class="you">You:</span>{/if}
                    {m.content}
                  </span>
                  {#if chat.unread > 0}
                    <span class="unread-pill" class:muted={chat.muted}>{chat.unread}</span>
                  {/if}
                </div>
              </div>
              <ChatActions {chat} />
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    <MockToolbar currentPage="chats" />
  </div>
</Auth>

<style>
  .mock-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .mock-container {
      padding-left: var(--sidenav-width);
    }
  }

  .mock-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
    box-sizing: border-box;
  }

  .mock-note {
    margin: 0 0 1rem;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .mock-note a {
    color: var(--primary);
  }

  .page-title {
    margin: 0 0 1rem;
    font-size: 1.4rem;
    color: var(--fg);
  }

  .section-title {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0 0 0.25rem;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .count {
    padding: 0 0.45rem;
    border-radius: 999px;
    background: var(--border);
    color: var(--fg);
    font-size: 0.75rem;
  }

  .section-hint {
    margin: 0 0 0.75rem;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .chat-list {
    list-style: none;
    margin: 0 0 1.5rem;
    padding: 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
  }

  .chat-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 0.75rem 0.75rem 1rem;
    cursor: pointer;
    border-bottom: 1px solid var(--border);
  }

  .chat-row:first-child {
    border-radius: 12px 12px 0 0;
  }

  .chat-row:last-child {
    border-bottom: none;
    border-radius: 0 0 12px 12px;
  }

  .chat-row:only-child {
    border-radius: 12px;
  }

  .chat-row:hover {
    background: var(--input-bg);
  }

  .chat-row.request {
    border-left: 3px solid var(--primary);
  }

  .chat-row.blocked {
    opacity: 0.6;
  }

  .chat-main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }

  .chat-top,
  .chat-bottom {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
  }

  .chat-name {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-weight: 600;
    color: var(--fg);
    min-width: 0;
  }

  .chat-row.unread .chat-name,
  .chat-row.unread .chat-preview {
    font-weight: 700;
    color: var(--fg);
  }

  .chat-time {
    color: var(--muted);
    font-size: 0.75rem;
    white-space: nowrap;
  }

  .chat-preview {
    color: var(--muted);
    font-size: 0.875rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }

  .you {
    color: var(--muted);
    font-weight: 500;
  }

  .flag {
    font-size: 0.75rem;
  }

  .flag.pin {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    background-color: var(--muted);
    -webkit-mask: url('/icons/pin-16-filled.png') center / contain no-repeat;
    mask: url('/icons/pin-16-filled.png') center / contain no-repeat;
  }

  .badge {
    padding: 0 0.4rem;
    border: 1px solid var(--error);
    border-radius: 999px;
    color: var(--error);
    font-size: 0.65rem;
    font-weight: 600;
  }

  .unread-pill {
    flex-shrink: 0;
    min-width: 1.2rem;
    padding: 0 0.35rem;
    border-radius: 999px;
    background: var(--primary);
    color: var(--button-text);
    font-size: 0.7rem;
    font-weight: 700;
    line-height: 1.2rem;
    text-align: center;
  }

  .unread-pill.muted {
    background: var(--border);
    color: var(--fg);
  }

  .empty {
    text-align: center;
    padding: 2rem 1rem;
    color: var(--fg);
  }

  @media (max-width: 768px) {
    .mock-content {
      padding: 0.5rem;
    }
  }
</style>
