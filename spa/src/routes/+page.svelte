<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import { requestPersistentStorage } from '$lib/services/pwa';
  import { isRecoveryMode, isSignupOpen, serverInfoLoading } from '$lib/services/serverInfo';

  $: showRecoveryLanding =
    !$serverInfoLoading && $isRecoveryMode && !authService.isLoggedIn();

  let deferredPrompt = null;
  let showInstallButton = false;
  let isPWAInstalled = false;

  onMount(async () => {
    if (authService.isLoggedIn()) {
      goto('/reeds');
      return;
    }

    // Request persistent storage first
    await requestPersistentStorage();

    // Check if PWA is already installed
    isPWAInstalled = window.matchMedia('(display-mode: standalone)').matches;

    // Listen for the beforeinstallprompt event
    window.addEventListener('beforeinstallprompt', (e) => {
      e.preventDefault();
      deferredPrompt = e;
      showInstallButton = true;
    });

    // Listen for appinstalled event
    window.addEventListener('appinstalled', () => {
      isPWAInstalled = true;
      showInstallButton = false;
      deferredPrompt = null;
    });
  });

  async function installApp() {
    if (deferredPrompt) {
      deferredPrompt.prompt();
      const { outcome } = await deferredPrompt.userChoice;
      console.log(`User response to the install prompt: ${outcome}`);
      deferredPrompt = null;
      showInstallButton = false;
    }
  }
</script>

<div class="container">
  <div class="card">
    <h1>Welcome to Syrinx</h1>
    {#if showRecoveryLanding}
      <div class="recovery-banner" role="status">
        <p>
          This server is in recovery mode. If you have Syrinx data on this device,
          report it back to help restore the server.
        </p>
      </div>
      <p class="subtitle">Report your local data to restore this server</p>
    {:else}
      <p class="subtitle">A distributed, P2P content-distribution platform</p>
    {/if}

    {#if showInstallButton && !isPWAInstalled}
      <div class="install-section">
        <button on:click={installApp} class="btn btn-install">
          📱 Install App
        </button>
        <p class="install-text">Install Syrinx to get started</p>
      </div>
    {:else if !isPWAInstalled}
      <div class="install-section">
        <p class="install-text">Syrinx is not installed. Please install it to continue.</p>
      </div>
    {/if}

    <div class="action-buttons">
      {#if showRecoveryLanding}
        <a href="/recover" class="btn btn-primary">Recover your account</a>
      {/if}
      {#if !$serverInfoLoading && $isSignupOpen}
        <a href="/preamble" class="btn {showRecoveryLanding ? 'btn-secondary' : 'btn-primary'}">
          Sign Up
        </a>
      {/if}
      <a href="/import" class="btn btn-secondary">Import backup</a>
    </div>
  </div>
</div>

<style>
  .container {
    max-width: 640px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: center;
  }

  .card h1 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 2rem;
  }

  .card p.subtitle {
    margin: 0 0 2rem 0;
    color: var(--muted);
    font-size: 1.1rem;
  }

  .recovery-banner {
    margin: 0 0 1.5rem 0;
    padding: 1rem;
    border-radius: 8px;
    background: linear-gradient(135deg, rgba(230, 126, 34, 0.15), rgba(214, 48, 49, 0.12));
    border: 1px solid rgba(230, 126, 34, 0.45);
    text-align: left;
  }

  .recovery-banner p {
    margin: 0;
    color: var(--fg);
    font-size: 0.95rem;
    line-height: 1.5;
  }

  .action-buttons {
    display: flex;
    gap: 1rem;
    justify-content: center;
    border-top: 1px solid var(--border);
    padding-top: 2rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    transition: all 0.2s ease;
    border: none;
    cursor: pointer;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--input-bg);
    transform: translateY(-1px);
  }

  .install-section {
    margin-top: 2rem;
    padding-top: 2rem;
    border-top: 1px solid var(--border);
  }

  .btn-install {
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    color: white;
    border: none;
    font-size: 1rem;
  }

  .btn-install:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .install-text {
    margin: 0.5rem 0 0 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  @media (max-width: 640px) {
    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }
  }
</style>
