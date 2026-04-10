<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';

  let user = null;
  let loading = true;

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();

      if (!user) {
        // Redirect to home page if no user
        window.location.href = '/';
        return;
      }
    } catch (error) {
      console.error('Error getting user:', error);
      // Redirect to home page on error
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });
</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading chats...</h2>
        <p>Please wait while we fetch your active conversations.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="chats-container">
    <!-- Main Content -->
    <div class="chats-content">
      <div class="chats-list">
        <!-- Chat Item 1 -->
        <div class="chat-item">
          <div class="chat-avatar">
            <div class="avatar">👤</div>
            <div class="status online"></div>
          </div>
          <div class="chat-info">
            <div class="chat-header">
              <h3>Support Team</h3>
              <span class="chat-time">5 min ago</span>
            </div>
            <div class="chat-preview">
              <p>Welcome to Syrinx! How can we help you get started?</p>
            </div>
          </div>
          <div class="chat-meta">
            <span class="unread-count">2</span>
          </div>
        </div>

        <!-- Chat Item 2 -->
        <div class="chat-item">
          <div class="chat-avatar">
            <div class="avatar">🔐</div>
            <div class="status away"></div>
          </div>
          <div class="chat-info">
            <div class="chat-header">
              <h3>Security Bot</h3>
              <span class="chat-time">1 hour ago</span>
            </div>
            <div class="chat-preview">
              <p>Your encryption keys are ready. Start chatting securely!</p>
            </div>
          </div>
          <div class="chat-meta">
            <span class="unread-count">0</span>
          </div>
        </div>

        <!-- Chat Item 3 -->
        <div class="chat-item">
          <div class="chat-avatar">
            <div class="avatar">🌐</div>
            <div class="status offline"></div>
          </div>
          <div class="chat-info">
            <div class="chat-header">
              <h3>Network Admin</h3>
              <span class="chat-time">2 hours ago</span>
            </div>
            <div class="chat-preview">
              <p>You're now connected to the Syrinx network. Happy messaging!</p>
            </div>
          </div>
          <div class="chat-meta">
            <span class="unread-count">0</span>
          </div>
        </div>

        <!-- Empty State -->
        <div class="empty-state">
          <div class="empty-icon">💬</div>
          <h3>No active chats</h3>
          <p>Start a conversation or wait for someone to message you.</p>
        </div>
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="chats" />
    </div>
  </Auth>
{/if}

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
    padding: 1rem;
  }

  .chats-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .chat-item {
    display: flex;
    align-items: center;
    gap: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
    transition: all 0.2s ease;
    cursor: pointer;
  }

  .chat-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .chat-avatar {
    position: relative;
    flex-shrink: 0;
  }

  .avatar {
    width: 50px;
    height: 50px;
    border-radius: 50%;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.5rem;
  }

  .status {
    position: absolute;
    bottom: 2px;
    right: 2px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    border: 2px solid var(--surface);
  }

  .status.online {
    background: #4caf50;
  }

  .status.away {
    background: #ff9800;
  }

  .status.offline {
    background: var(--muted);
  }

  .chat-info {
    flex: 1;
    min-width: 0;
  }

  .chat-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.25rem;
  }

  .chat-header h3 {
    margin: 0;
    color: var(--fg);
    font-size: 1rem;
    font-weight: 600;
  }

  .chat-time {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .chat-preview {
    margin: 0;
  }

  .chat-preview p {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.3;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .chat-meta {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  .unread-count {
    background: var(--primary);
    color: var(--button-text);
    padding: 0.25rem 0.5rem;
    border-radius: 12px;
    font-size: 0.75rem;
    font-weight: 600;
    min-width: 20px;
    text-align: center;
  }

  .unread-count:empty {
    display: none;
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
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }


  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
  }

  /* Responsive Design */
  @media (max-width: 768px) {
    .chats-content {
      padding: 0.5rem;
    }

    .chat-item {
      padding: 0.75rem;
    }

    .avatar {
      width: 40px;
      height: 40px;
      font-size: 1.2rem;
    }

  }
</style>
