<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import { requestPersistentStorage } from '$lib/services/pwa';
  import {
    isSignupClosed,
    isSignupOpen,
    serverInfoLoading,
  } from '$lib/services/serverInfo';
  import { get } from 'svelte/store';

  let deferredPrompt = null;
  let showInstallButton = false;
  let isPWAInstalled = false;
  let gateReady = false;

  onMount(async () => {
    if (authService.isLoggedIn()) {
      const user = await authService.getCurrentUser();
      if (user) {
        goto('/reeds');
        return;
      }
    }

    const waitForInfo = () =>
      new Promise((resolve) => {
        if (!get(serverInfoLoading)) {
          resolve();
          return;
        }
        const unsub = serverInfoLoading.subscribe((loadingInfo) => {
          if (!loadingInfo) {
            unsub();
            resolve();
          }
        });
      });
    await waitForInfo();
    gateReady = true;

    // Invite links go to /signup?iid=&uid=…; preamble is only for the open home CTA.
    if (get(isSignupClosed) || !get(isSignupOpen)) {
      return;
    }

    await requestPersistentStorage();

    isPWAInstalled = window.matchMedia('(display-mode: standalone)').matches;

    window.addEventListener('beforeinstallprompt', (e) => {
      e.preventDefault();
      deferredPrompt = e;
      showInstallButton = true;
    });

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
    {#if !gateReady || $serverInfoLoading}
      <p class="gate-message">Checking signup availability…</p>
    {:else if $isSignupClosed}
      <h1>Signups closed</h1>
      <p class="gate-message">This server is not accepting new signups.</p>
      <a href="/" class="btn btn-primary">Back to home</a>
    {:else if !$isSignupOpen}
      <h1>Invite required</h1>
      <p class="gate-message">
        This server requires an invite link to join. Open the link you were
        given, or go back home.
      </p>
      <a href="/" class="btn btn-primary">Back to home</a>
    {:else}
      <h1>Please read carefully</h1>

      <div class="content">
        <p class="intro">
          This is a <strong>Free</strong> (<a href="https://en.wikipedia.org/wiki/The_Free_Software_Definition">as defined by the Free Software Foundation</a>)
          web app, built on an open platform. You can inspect it and audit it, which is great for you as a user, but this also means that there are some
          inherent security &amp; privacy challenges to be aware of.
        </p>

        <div class="warning-section">
          <h2>Security &amp; Privacy Considerations</h2>

          <div class="warning-item">
            <h3>🛠️ Install the App</h3>
            <p>
              We strongly suggest installing the app before continuing. <strong>Browser extensions can
              read all your data</strong> unless the app is installed.
            </p>
          </div>

          <div class="warning-item">
            <h3>🔐 Backup your Key</h3>
            <p>
              You will notice that the app requires no password to be used. Instead it uses a private key
              to encrypt and decrypt your messages that is stored locally on your device. If you lose this
              key you won't be able to access your account anymore.
            </p>
          </div>

          <div class="warning-item">
            <h3>🌐 Data Permanence</h3>
            <p>
              The data you publish in the platform is not stored on the server, but on the users' devices.
              Think of it like a torrent. Once something is published there's no guarantee that it can be
              deleted. Assume that anything you publish will live somewhere forever.
            </p>
          </div>
        </div>
      </div>

      <p><strong>Important:</strong> If you create a user and later decide to install the app, you won't have
      access to that data anymore due to browser-enforced security restrictions. So either install the app first or
      use it as a web app, but migration won't be possible.</p>
      <div class="action-buttons">
        {#if !showInstallButton}
          <p class="error">App installation not available on this device, sorry</p>
        {:else}
          <button on:click={installApp} class="btn btn-install" disabled={!showInstallButton || isPWAInstalled}>
            Install App
          </button>
        {/if}
        <a href="/signup" class="btn btn-primary">I Understand, Continue to Sign Up</a>
      </div>
    {/if}
  </div>
</div>

<style>
  .container {
    max-width: 800px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: left;
  }

  .card h1 {
    margin: 0 0 1.5rem 0;
    color: var(--fg);
    font-size: 2rem;
    text-align: center;
  }

  .content {
    margin-bottom: 2rem;
  }

  .intro {
    font-size: 1.1rem;
    line-height: 1.6;
    margin-bottom: 2rem;
    text-align: center;
  }

  .warning-section {
    margin-bottom: 2rem;
  }

  .warning-section h2 {
    color: #f59e0b;
    font-size: 1.3rem;
    margin-bottom: 1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item {
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: rgba(245, 158, 11, 0.1);
    border-left: 4px solid #f59e0b;
    border-radius: 8px;
  }

  .warning-item h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item p {
    margin: 0;
    line-height: 1.5;
  }

  .gate-message {
    color: var(--muted);
    line-height: 1.5;
    text-align: center;
  }

  .action-buttons {
    border-top: 1px solid var(--border);
    padding-top: 2rem;
    text-align: center;
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
    font-size: 0.95rem;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-install {
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    color: white;
    border: none;
    font-size: 0.95rem;
  }

  .btn-install:hover:not(:disabled) {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-install:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  @media (max-width: 640px) {
    .container {
      padding: 0.5rem;
    }

    .card {
      padding: 1.5rem;
    }

    .card h1 {
      font-size: 1.5rem;
    }

    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }

    .warning-item {
      padding: 0.75rem;
    }

    .warning-item h3 {
      font-size: 1rem;
    }
  }
</style>
