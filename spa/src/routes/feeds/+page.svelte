<script>
  import { onMount, onDestroy } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';

  let user = null;
  let loading = true;
  let activeSection = 'broadcast'; // 'broadcast' or 'followcast'

  function handleReedNotification(data) {
    console.log('ServerEvent.ReedNotification:', data);
  }

  function setActiveSection(section) {
    activeSection = section;
  }

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();
    } catch (error) {
      console.error('Error getting user:', error);
    } finally {
      loading = false;
    }

    serverConnection.on(ServerEvent.ReedNotification, handleReedNotification);
    await serverConnection.connect();
    serverConnection.subscribeToBroadcast();
  });

  onDestroy(() => {
    serverConnection.off(ServerEvent.ReedNotification, handleReedNotification);
    serverConnection.unsubscribeFromBroadcast();
  });
</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading feeds...</h2>
        <p>Please wait while we fetch your latest updates.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="feeds-container">
    <!-- Section Toggle -->
    <div class="section-toggle">
      <button
        class="toggle-btn"
        class:active={activeSection === 'broadcast'}
        on:click={() => setActiveSection('broadcast')}
      >
      <!--
        Broadcast: You get everything that is posted on the platform, but it
        doesn't get persisted to IndexedDB, only to SessionStorage (that is,
        until you click on it).
      -->
        📡 Broadcast
      </button>
      <button
        class="toggle-btn"
        class:active={activeSection === 'followcast'}
        on:click={() => setActiveSection('followcast')}
      >
      <!--
        Followcast: You only get messages from the people you follow, but they
        get automatically persisted. This also means that you will broadcast
        them to whoever requests them too.
      -->
        👥 Followcast
      </button>
    </div>

    <!-- Main Content -->
    <div class="feeds-content">
      <div class="feeds-list">
        {#if activeSection === 'broadcast'}
          <!-- Broadcast Section -->
          <div class="section-header">
            <h2>📡 Broadcast</h2>
            <p>Public messages and announcements</p>
          </div>

          <div class="waiting-state">
            <div class="waiting-pulse"></div>
            <p>Listening for new reeds...</p>
          </div>
        {:else}
          <!-- Followcast Section -->
          <div class="section-header">
            <h2>👥 Followcast</h2>
            <p>Messages from people you follow</p>
          </div>

          <!-- Followcast Feed Item 1 -->
          <div class="feed-item">
            <div class="feed-header">
              <div class="feed-author">
                <div class="avatar">👤</div>
                <div class="author-info">
                  <span class="author-name">Alice Johnson</span>
                  <span class="feed-time">5 minutes ago</span>
                </div>
              </div>
              <div class="feed-actions">
                <button class="action-btn">⋯</button>
              </div>
            </div>
            <div class="feed-content">
              <p>Just finished setting up my new secure messaging setup. The encryption is working perfectly!</p>
            </div>
            <div class="feed-footer">
              <button class="interaction-btn">👍 Like</button>
              <button class="interaction-btn">💬 Reply</button>
              <button class="interaction-btn">🔄 Share</button>
            </div>
          </div>

          <!-- Followcast Feed Item 2 -->
          <div class="feed-item">
            <div class="feed-header">
              <div class="feed-author">
                <div class="avatar">🌐</div>
                <div class="author-info">
                  <span class="author-name">Bob Smith</span>
                  <span class="feed-time">1 hour ago</span>
                </div>
              </div>
              <div class="feed-actions">
                <button class="action-btn">⋯</button>
              </div>
            </div>
            <div class="feed-content">
              <p>Excited to be part of the Syrinx community! Looking forward to secure conversations with everyone.</p>
            </div>
            <div class="feed-footer">
              <button class="interaction-btn">👍 Like</button>
              <button class="interaction-btn">💬 Reply</button>
              <button class="interaction-btn">🔄 Share</button>
            </div>
          </div>

          <!-- Empty State for Followcast -->
          <div class="empty-state">
            <div class="empty-icon">👥</div>
            <h3>No followcast messages</h3>
            <p>Start following people to see their messages here.</p>
          </div>
        {/if}
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="feeds" />
    </div>
  </Auth>
{/if}

<style>
  .feeds-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .section-toggle {
    display: flex;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0.5rem;
    gap: 0.25rem;
    position: sticky;
    top: 0;
    z-index: 10;
  }

  .toggle-btn {
    flex: 1;
    background: transparent;
    border: 1px solid var(--border);
    color: var(--muted);
    padding: 0.75rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.2s ease;
    font-size: 0.9rem;
    font-weight: 500;
  }

  .toggle-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
    border-color: var(--primary);
  }

  .toggle-btn.active {
    background: var(--primary);
    color: var(--button-text);
    border-color: var(--primary);
  }

  .section-header {
    padding: 1rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    margin-bottom: 1rem;
  }

  .section-header h2 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1.1rem;
    font-weight: 600;
  }

  .section-header p {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }


  .feeds-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .feeds-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .feed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
  }

  .feed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .feed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .feed-author {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .avatar {
    width: 40px;
    height: 40px;
    border-radius: 50%;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.2rem;
  }

  .author-info {
    display: flex;
    flex-direction: column;
  }

  .author-name {
    font-weight: 600;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .feed-time {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .feed-actions {
    display: flex;
    gap: 0.5rem;
  }

  .action-btn {
    background: transparent;
    border: none;
    color: var(--muted);
    cursor: pointer;
    padding: 0.5rem;
    border-radius: 4px;
    transition: all 0.2s ease;
  }

  .action-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .feed-content {
    padding: 1rem;
  }

  .feed-content p {
    margin: 0;
    color: var(--fg);
    line-height: 1.5;
  }

  .feed-footer {
    display: flex;
    gap: 1rem;
    padding: 0.75rem 1rem;
    border-top: 1px solid var(--border);
    background: var(--input-bg);
  }

  .interaction-btn {
    background: transparent;
    border: none;
    color: var(--muted);
    cursor: pointer;
    padding: 0.5rem 0.75rem;
    border-radius: 6px;
    transition: all 0.2s ease;
    font-size: 0.9rem;
  }

  .interaction-btn:hover {
    background: var(--surface);
    color: var(--fg);
  }

  .waiting-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 1rem;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .waiting-pulse {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--primary);
    animation: pulse 1.5s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; transform: scale(1); }
    50% { opacity: 0.4; transform: scale(0.8); }
  }

  .waiting-state p {
    margin: 0;
    font-size: 0.9rem;
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
    .feeds-content {
      padding: 0.5rem;
    }

    .feed-header {
      padding: 0.75rem;
    }

    .feed-content {
      padding: 0.75rem;
    }

    .feed-footer {
      padding: 0.5rem 0.75rem;
    }

  }
</style>
