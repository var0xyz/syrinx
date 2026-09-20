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
      <p class="subtitle">A P2P content-distribution platform.</p>
    {/if}

      <div class="install-section">
        <button on:click={installApp} class="btn btn-install">
          <span class="install-icon"></span>Install App
        </button>
      </div>

    <div class="action-buttons">
      <a href="/import" class="btn btn-primary">Import User</a>
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
    padding-top: 1rem;
    text-align: center;
  }

  .card h1 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 2rem;
  }

  .card p.subtitle {
    margin-bottom: 1rem;
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
    justify-content: space-between;
    padding-top: 1rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    transition: all 0.2s ease;
    border: none;
    cursor: pointer;
    white-space: nowrap;
    width: 100%;
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
    margin: 2rem auto 0;
  }

  .install-icon {
    display: inline-block;
    width: 1rem;
    height: 1rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
    -webkit-mask-image: url('/icons/install-16.png');
    mask-image: url('/icons/install-16.png');
    margin-right: 0.5rem;
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

  @media (max-width: 640px) {
    .action-buttons {
      flex-direction: column;
    }

    .install-section {
      max-width: 100%;
    }

    .btn {
      text-align: center;
    }
  }
</style>
