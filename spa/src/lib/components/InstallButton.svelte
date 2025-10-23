<script lang="ts">
  import { onMount } from 'svelte';
  import { canInstall, isInstalled } from '$lib/services/pwa';
  import { installPWA } from '$lib/services/pwa';

  let showButton = false;
  let isInstalling = false;

  onMount(() => {
    // Subscribe to install state changes
    const unsubscribeCanInstall = canInstall.subscribe((value) => {
      showButton = value;
    });

    const unsubscribeIsInstalled = isInstalled.subscribe((value) => {
      if (value) {
        showButton = false;
      }
    });

    return () => {
      unsubscribeCanInstall();
      unsubscribeIsInstalled();
    };
  });

  async function handleInstall() {
    if (isInstalling) return;

    isInstalling = true;
    try {
      const success = await installPWA();
      if (success) {
        showButton = false;
      }
    } catch (error) {
      console.error('Install failed:', error);
    } finally {
      isInstalling = false;
    }
  }
</script>

{#if showButton}
  <div class="install-banner">
    <div class="install-content">
      <div class="install-icon">📱</div>
      <div class="install-text">
        <h4>Install Syrinx</h4>
        <p>Get the full app experience on your device</p>
      </div>
      <button
        class="install-button"
        on:click={handleInstall}
        disabled={isInstalling}
      >
        {isInstalling ? 'Installing...' : 'Install'}
      </button>
      <button
        class="dismiss-button"
        on:click={() => showButton = false}
        aria-label="Dismiss install prompt"
      >
        ✕
      </button>
    </div>
  </div>
{/if}

<style>
  .install-banner {
    position: fixed;
    bottom: 1rem;
    left: 1rem;
    right: 1rem;
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    color: white;
    border-radius: 12px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
    z-index: 1000;
    animation: slideUp 0.3s ease-out;
    max-width: 500px;
    margin: 0 auto;
  }

  .install-content {
    display: flex;
    align-items: center;
    padding: 1rem;
    gap: 0.75rem;
  }

  .install-icon {
    font-size: 1.5rem;
    flex-shrink: 0;
  }

  .install-text {
    flex: 1;
    min-width: 0;
  }

  .install-text h4 {
    margin: 0 0 0.25rem 0;
    font-size: 1rem;
    font-weight: 600;
  }

  .install-text p {
    margin: 0;
    font-size: 0.85rem;
    opacity: 0.9;
    line-height: 1.3;
  }

  .install-button {
    background: rgba(255, 255, 255, 0.2);
    border: 1px solid rgba(255, 255, 255, 0.3);
    color: white;
    padding: 0.5rem 1rem;
    border-radius: 6px;
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.2s ease;
    flex-shrink: 0;
  }

  .install-button:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.3);
    transform: translateY(-1px);
  }

  .install-button:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .dismiss-button {
    background: none;
    border: none;
    color: white;
    font-size: 1.2rem;
    cursor: pointer;
    padding: 0.25rem;
    border-radius: 4px;
    transition: background-color 0.2s ease;
    flex-shrink: 0;
    opacity: 0.7;
  }

  .dismiss-button:hover {
    background: rgba(255, 255, 255, 0.2);
    opacity: 1;
  }

  @keyframes slideUp {
    from {
      transform: translateY(100%);
      opacity: 0;
    }
    to {
      transform: translateY(0);
      opacity: 1;
    }
  }

  @media (max-width: 640px) {
    .install-banner {
      left: 0.5rem;
      right: 0.5rem;
      bottom: 0.5rem;
    }

    .install-content {
      padding: 0.75rem;
      gap: 0.5rem;
    }

    .install-text h4 {
      font-size: 0.9rem;
    }

    .install-text p {
      font-size: 0.8rem;
    }

    .install-button {
      padding: 0.4rem 0.8rem;
      font-size: 0.8rem;
    }
  }
</style>
