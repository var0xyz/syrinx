<script>
  import { onMount, onDestroy } from 'svelte';
  import { notificationStore } from '$lib/stores/notifications';

  let notifications = [];
  let dismissing = new Set();
  let unsubscribe;

  $: dismissingArray = Array.from(dismissing);

  onMount(() => {
    unsubscribe = notificationStore.subscribe((newNotifications) => {
      notifications = newNotifications;
    });


    return () => {
      if (unsubscribe) unsubscribe();
    };
  });

  onDestroy(() => {
    if (unsubscribe) {
      unsubscribe();
    }
  });

  function dismissNotification(id) {
    dismissing.add(id);
    setTimeout(() => {
      notificationStore.dismiss(id);
    }, 300);
  }

  function pauseNotification(id) {
    notificationStore.pause(id);
  }

  function resumeNotification(id) {
    notificationStore.resume(id);
  }
</script>

<div class="notifications-container">
  {#each notifications as notification (notification.id)}
    {#if notification.type === 'info'}
      <button
        type="button"
        class="notification notification-info clickable"
        class:dismissing={dismissingArray.includes(notification.id)}
        on:click={() => dismissNotification(notification.id)}
      >
        <div class="notification-content">
          <div class="notification-message">
            {notification.message}
          </div>
        </div>
        <div class="notification-progress">
          <div
            class="notification-progress-bar"
            style="animation-duration: {notification.duration}ms"
          ></div>
        </div>
      </button>
    {:else}
      <div
        class="notification"
        class:notification-error={notification.type === 'error'}
        class:notification-warning={notification.type === 'warning'}
        class:notification-success={notification.type === 'success'}
        class:dismissing={dismissingArray.includes(notification.id)}
        role="status"
      >
        <div class="notification-content">
          <div class="notification-message">
            {notification.message}
          </div>
          {#if notification.type === 'error' || notification.type === 'warning'}
            <button
              type="button"
              class="notification-dismiss"
              on:click={() => dismissNotification(notification.id)}
              aria-label="Dismiss notification"
            >
              ✕
            </button>
          {/if}
        </div>

        {#if notification.type === 'success'}
          <button
            type="button"
            class="notification-progress"
            aria-label="Pause notification timer"
            on:click={() => pauseNotification(notification.id)}
            on:mouseenter={() => pauseNotification(notification.id)}
            on:mouseleave={() => resumeNotification(notification.id)}
          >
            <div
              class="notification-progress-bar"
              style="animation-duration: {notification.duration}ms"
            ></div>
          </button>
        {/if}
      </div>
    {/if}
  {/each}
</div>

<style>
  .notifications-container {
    position: fixed;
    top: 1rem;
    right: 1rem;
    z-index: 1000;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-width: 400px;
    pointer-events: none;
  }

  .notification {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    overflow: hidden;
    pointer-events: auto;
    transform: translateX(100%);
    animation: slideIn 0.3s ease-out forwards;
    max-width: 100%;
    touch-action: pan-x;
  }

  button.notification {
    display: block;
    width: 100%;
    text-align: left;
    font: inherit;
    color: inherit;
    padding: 0;
    margin: 0;
    appearance: none;
    -webkit-appearance: none;
  }

  .notification.dismissing {
    animation: slideOut 0.3s ease-in forwards !important;
  }

  .notification-content {
    display: flex;
    align-items: center;
    padding: 1rem;
    gap: 0.75rem;
  }

  .notification-message {
    flex: 1;
    font-size: 0.875rem;
    line-height: 1.4;
    color: var(--fg);
  }

  .notification-dismiss {
    background: none;
    border: none;
    color: var(--muted);
    cursor: pointer;
    padding: 0.25rem;
    border-radius: 4px;
    font-size: 1rem;
    line-height: 1;
    transition: all 0.2s ease;
    max-width: 2rem;
  }

  .notification-dismiss:hover {
    background: var(--border);
    color: var(--fg);
  }

  .notification-progress {
    display: block;
    width: 100%;
    height: 3px;
    padding: 0;
    margin: 0;
    border: none;
    background: var(--border);
    position: relative;
    cursor: pointer;
  }

  .notification-progress-bar {
    height: 100%;
    background: var(--muted);
    width: 100%;
    transform-origin: left;
    animation: progressBar linear forwards;
  }

  /* Type-specific styling */
  .notification-error {
    border-left: 4px solid #dc2626;
  }

  .notification-warning {
    border-left: 4px solid #f59e0b;
  }

  .notification-info {
    border-left: 4px solid #3b82f6;
  }

  .notification.clickable {
    cursor: pointer;
  }

  .notification-success {
    border-left: 4px solid #059669;
  }

  .notification-error .notification-progress-bar {
    background: #dc2626;
  }

  .notification-warning .notification-progress-bar {
    background: #f59e0b;
  }

  .notification-info .notification-progress-bar {
    background: #3b82f6;
  }

  .notification-success .notification-progress-bar {
    background: #059669;
  }

  @keyframes slideIn {
    from {
      transform: translateX(100%);
      opacity: 0;
    }
    to {
      transform: translateX(0);
      opacity: 1;
    }
  }

  @keyframes slideOut {
    from {
      transform: translateX(0);
      opacity: 1;
    }
    to {
      transform: translateX(100%);
      opacity: 0;
    }
  }

  @keyframes progressBar {
    from {
      transform: scaleX(1);
    }
    to {
      transform: scaleX(0);
    }
  }

  @media (max-width: 640px) {
    .notifications-container {
      right: 0.5rem;
      left: 0.5rem;
      max-width: none;
    }
  }
</style>
