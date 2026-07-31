<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { get } from 'svelte/store';
  import { requestSigner } from '../services/request-signer';
  import { authService } from '../services/auth';
  import {
    enforceImportGate,
    isImportGated,
  } from '../services/restoreFlow';
  import { isImportInProgress } from '../services/importRun';

  export let children;

  // Layout load() already resolved the user before this page rendered.
  let isChecking = !get(page).data?.user;

  onMount(async () => {
    await checkAuthentication();
  });

  async function checkAuthentication() {
    try {
      if (isImportInProgress() || isImportGated()) {
        enforceImportGate(window.location.pathname);
        return;
      }

      if (!authService.isLoggedIn()) {
        goto('/signup');
        return;
      }

      const user = get(page).data?.user ?? (await authService.getCurrentUser());

      if (!user) {
        goto('/signup');
        return;
      }

      const passphrase = authService.getPassphrase();
      const fingerprint = authService.getActiveKeyFingerprint();

      if (fingerprint && passphrase && !requestSigner.isInitialized()) {
        try {
          await requestSigner.initializeWorker(fingerprint, passphrase);
        } catch (error) {
          console.warn('Failed to auto-initialize request signer:', error);
          goto('/signup');
          return;
        }
      }

      isChecking = false;
    } catch (error) {
      console.error('Authentication check failed:', error);
      goto('/signup');
    }
  }
</script>

{#if isChecking}
  <div class="loading">
    <div class="spinner"></div>
    <p>Checking authentication...</p>
  </div>
{:else}
  <div class="auth-container">
    <div class="auth-content">
      {@render children()}
    </div>
  </div>
{/if}

<style>
  .loading {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: 200px;
    gap: 1rem;
  }

  .spinner {
    width: 32px;
    height: 32px;
    border: 3px solid var(--border-color);
    border-top: 3px solid var(--accent-color);
    border-radius: 50%;
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    0% { transform: rotate(0deg); }
    100% { transform: rotate(360deg); }
  }

  .loading p {
    color: var(--text-secondary);
    margin: 0;
  }

  .auth-container {
    display: flex;
    flex-direction: column;
  }

  .auth-content {
    flex: 1;
  }
</style>
