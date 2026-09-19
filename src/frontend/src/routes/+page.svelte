<script>
  import { onMount } from 'svelte';
  import { requestPersistentStorage, canInstall, isInstalled, installPWA } from '$lib/services/pwa';
  import { redirectForRestoreState } from '$lib/services/restoreFlow';
  import { isRecoveryMode, isSignupOpen, serverInfoLoading } from '$lib/services/serverInfo';

  $: showRecoveryBanner = !$serverInfoLoading && $isRecoveryMode;

  onMount(async () => {
    if (await redirectForRestoreState()) {
      return;
    }

    await requestPersistentStorage();
  });

  async function installApp() {
    await installPWA();
  }
</script>

<div class="container">
  <div class="card">
    <h1>Welcome to Syrinx</h1>
    {#if showRecoveryBanner}
      <div class="recovery-banner" role="status">
        <p>
          This server is rebuilding. Restore from an encrypted backup created on
          your previous Syrinx app — this device does not yet hold your data.
        </p>
      </div>
      <p class="subtitle">Restore from a backup to continue</p>
    {:else}
      <p class="subtitle">A distributed, P2P content-distribution platform</p>
    {/if}

    {#if $canInstall && !$isInstalled}
      <div class="install-section">
        <button on:click={installApp} class="btn btn-install">
          📱 Install App
        </button>
        <p class="install-text">Install Syrinx to get started</p>
      </div>
    {/if}

    <div class="action-buttons">
      <a href="/import" class="btn btn-primary">Already a user</a>
      {#if !$serverInfoLoading && $isSignupOpen && !$isRecoveryMode}
        <a href="/signup" class="btn btn-secondary">Sign Up</a>
      {/if}
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
