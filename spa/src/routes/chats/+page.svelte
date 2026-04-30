<script lang="ts">
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { chatRepository } from '$lib/repositories/chat';
  import { userRepository } from '$lib/repositories/user';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';

  let loading = true;
  let chats = [];
  const serverId = localStorage.getItem('serverId') ?? '';

  onMount(async () => {
    const user = await authService.getCurrentUser();
    if (!user) { loading = false; return; }

    const raw = await chatRepository.getAll();
    const resolved = await Promise.all(raw.map(async (chat) => {
      const other = await userRepository.getByUserId(chat.userId).catch(() => null);
      return { chat, other };
    }));

    const rank = (c: { confirmed: boolean; pending?: boolean }) =>
      !c.confirmed ? 0 : c.pending ? 1 : 2;
    chats = resolved.sort((a, b) => rank(a.chat) - rank(b.chat));
    loading = false;
  });
</script>

<Auth>
  <div class="chats-container">
    <div class="chats-content">
      {#if loading}
        <p class="state-msg">Loading…</p>
      {:else if chats.length === 0}
        <div class="empty-state">
          <div class="empty-icon">💬</div>
          <h3>No chats yet</h3>
          <p>Visit someone's profile to start a conversation.</p>
        </div>
      {:else}
        <div class="chats-list">
          {#each chats as { chat, other }}
            <a class="chat-item" href="/chat/{serverId}/{chat.userId}">
              <div class="chat-avatar">
                {#if other?.avatarURL}
                  <img src={other.avatarURL} alt="" class="avatar" />
                {:else}
                  <div class="avatar-placeholder">👤</div>
                {/if}
              </div>
              <div class="chat-info">
                <span class="chat-username">{other?.username ?? chat.userId}</span>
                {#if !chat.confirmed}
                  <span class="request-chip">Chat request</span>
                {:else if chat.pending}
                  <span class="pending-chip">Awaiting approval</span>
                {/if}
              </div>
            </a>
          {/each}
        </div>
      {/if}
    </div>

    <BottomToolbar currentPage="chats" />
  </div>
</Auth>

<style>
  .chats-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .chats-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
  }

  .state-msg {
    color: var(--muted);
    text-align: center;
    padding: 2rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }

  .chats-list {
    display: flex;
    flex-direction: column;
  }

  .chat-item {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 1rem;
    border-bottom: 1px solid var(--border);
    text-decoration: none;
    color: var(--fg);
    transition: border-color 0.2s;
  }

  .chat-item:hover {
    border-color: var(--primary);
  }

  .chat-avatar {
    height: 44px;
  }

  .avatar {
    width: 44px;
    height: 44px;
    border-radius: 8px;
    object-fit: cover;
  }

  .avatar-placeholder {
    width: 44px;
    height: 44px;
    border-radius: 50%;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.4rem;
    flex-shrink: 0;
  }

  .chat-info {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .chat-username {
    font-weight: 600;
    font-size: 1rem;
  }

  .request-chip {
    font-size: 0.75rem;
    background: var(--primary);
    color: var(--button-text);
    padding: 0.15rem 0.5rem;
    border-radius: 99px;
    align-self: flex-start;
  }

  .pending-chip {
    font-size: 0.75rem;
    background: var(--input-bg);
    color: var(--muted);
    border: 1px solid var(--border);
    padding: 0.15rem 0.5rem;
    border-radius: 99px;
    align-self: flex-start;
  }
</style>
