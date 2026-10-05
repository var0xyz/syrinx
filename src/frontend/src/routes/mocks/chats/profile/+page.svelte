<script>
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import MockSideNav from '../MockSideNav.svelte';
  import MockToolbar from '../MockToolbar.svelte';
  import { chats, people } from '../data.js';

  $: userID = $page.url.searchParams.get('u') ?? '12@syrinx.local';
  $: person = people[userID] ?? { userID, username: userID, bio: '' };
  $: chat = $chats.find((c) => c.userID === userID);

  let following = true;
</script>

<Auth>
  <MockSideNav />
  <div class="mock-container">
    <div class="mock-content">
      <p class="mock-note">
        Chats mockup: profile. Chat sits next to Follow and opens the private conversation.
        <a href="/mocks/chats">All mockups</a>
      </p>

      <div class="profile-card">
        <div class="profile-header">
          <Avatar userID={person.userID} username={person.username} size="80px" />
          <div class="profile-info">
            <h2>{person.username}</h2>
            <p class="user-info">{person.userID}</p>
            <div class="follow-stats">
              <span><strong>42</strong> Following</span>
              <span><strong>17</strong> Followers</span>
            </div>
          </div>
        </div>
        {#if person.bio}
          <div class="user-bio"><MarkdownParser text={person.bio} /></div>
        {/if}
        <div class="profile-actions">
          <button class="action-btn" class:primary={!following} class:secondary={following} on:click={() => (following = !following)}>
            {following ? 'Unfollow' : 'Follow'}
          </button>
          {#if chat?.blocked}
            <button class="action-btn secondary" disabled title="You blocked {person.username}">Blocked</button>
          {:else}
            <button class="action-btn secondary chat-btn" on:click={() => goto(`/mocks/chats/with/${person.userID}`)}>
              <span class="chat-icon" aria-hidden="true">🗨️</span>
              Chat
              {#if chat && chat.unread > 0 && !chat.deleted}<span class="unread-pill">{chat.unread}</span>{/if}
            </button>
          {/if}
        </div>
      </div>
    </div>
    <MockToolbar />
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

  .profile-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
  }

  .profile-header {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }

  .profile-info {
    flex: 1;
    min-width: 0;
  }

  .profile-info h2 {
    margin: 0;
    color: var(--fg);
  }

  .user-info {
    margin: 0;
    color: var(--muted);
    font-family: monospace;
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  .follow-stats {
    display: flex;
    gap: 1rem;
    margin-top: 0.5rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .follow-stats strong {
    color: var(--fg);
  }

  .user-bio {
    font-size: 0.9rem;
    line-height: 1.4;
    margin-top: 0.5rem;
  }

  .profile-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 1rem;
  }

  .action-btn {
    padding: 0.5rem 1rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    transition: all 0.2s ease;
  }

  .action-btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .action-btn.secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .action-btn.secondary:hover:not(:disabled) {
    background: var(--border);
  }

  .action-btn:disabled {
    cursor: not-allowed;
    color: var(--muted);
  }

  .chat-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.4rem;
  }

  .unread-pill {
    min-width: 1.2rem;
    padding: 0 0.35rem;
    border-radius: 999px;
    background: var(--error);
    color: #fff;
    font-size: 0.7rem;
    line-height: 1.2rem;
  }

  @media (max-width: 768px) {
    .mock-content {
      padding: 0.5rem;
    }

    .profile-card {
      padding: 0.75rem;
    }

    .profile-actions {
      flex-direction: column;
      gap: 0.5rem;
    }
  }
</style>
