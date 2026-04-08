<script lang="ts">
  import { onMount } from 'svelte';

  let isOnline = true;
  let showOfflineMessage = false;
  let showOnlineMessage = false;
  let offlineTimer: number | null = null;
  let onlineTimer: number | null = null;

  onMount(() => {
    // Check initial online status
    isOnline = navigator.onLine;

    // Listen for online/offline events
    const handleOnline = () => {
      isOnline = true;
      showOfflineMessage = false;
      showOnlineMessage = true;

      // Clear any existing timers
      if (offlineTimer) clearTimeout(offlineTimer);
      if (onlineTimer) clearTimeout(onlineTimer);

      // Auto-hide the online message after 5 seconds
      onlineTimer = setTimeout(() => {
        showOnlineMessage = false;
      }, 5000);
    };

    const handleOffline = () => {
      isOnline = false;
      showOfflineMessage = true;
      showOnlineMessage = false;

      // Clear any existing timers
      if (offlineTimer) clearTimeout(offlineTimer);
      if (onlineTimer) clearTimeout(onlineTimer);

      // Auto-hide the offline message after 5 seconds
      offlineTimer = setTimeout(() => {
        showOfflineMessage = false;
      }, 5000);
    };

    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);

    return () => {
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);

      // Clear any pending timers
      if (offlineTimer) clearTimeout(offlineTimer);
      if (onlineTimer) clearTimeout(onlineTimer);
    };
  });
</script>

{#if showOfflineMessage}
  <div class="offline-indicator">
    <p>You're offline. Your data will sync when you're back online.</p>
  </div>
{/if}

{#if showOnlineMessage}
  <div class="online-indicator">
    <p>You're back online!</p>
  </div>
{/if}

<style>
  .offline-indicator {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    background: linear-gradient(135deg, #ff6b6b, #ee5a24);
    color: white;
    padding: 0.5rem 1rem;
    text-align: center;
    z-index: 1003;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
    animation: slideDown 0.3s ease-out;
  }

  .online-indicator {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    background: linear-gradient(135deg, #00b894, #00a085);
    color: white;
    padding: 1rem;
    text-align: center;
    z-index: 1003;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
    animation: slideDown 0.3s ease-out;
  }

  .offline-indicator p,
  .online-indicator p {
    margin: 0;
    font-size: 0.9rem;
    opacity: 0.9;
  }

  @keyframes slideDown {
    from {
      transform: translateY(-100%);
    }
    to {
      transform: translateY(0);
    }
  }

  @media (max-width: 768px) {
    .offline-indicator p,
    .online-indicator p {
      font-size: 0.8rem;
    }
  }
</style>
