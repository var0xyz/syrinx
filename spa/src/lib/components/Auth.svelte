<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { requestSigner } from '../services/request-signer';
  import { authService } from '../services/auth';
  import {
    enforceImportGate,
    isImportGated,
  } from '../services/restoreFlow';
  import { isImportInProgress } from '../services/importRun';

  export let children;

  let isChecking = true;

  onMount(async () => {
    await checkAuthentication();
  });

  async function checkAuthentication() {
    try {
      // Mid-import / mid-recovery: never treat local identity as a finished session.
      if (isImportInProgress() || isImportGated()) {
        enforceImportGate(window.location.pathname);
        return;
      }

      if (!authService.isLoggedIn()) {
        goto('/signup');
        return;
      }

      const user = await authService.getCurrentUser();

      if (!user) {
        goto('/signup');
        return;
      }

      const passphrase = authService.getPassphrase();
      const fingerprint = authService.getActiveKeyFingerprint();

      console.log('Auth check - fingerprint:', fingerprint);
      console.log('Auth check - passphrase:', passphrase ? '[REDACTED]' : 'null');
      console.log('Auth check - request signer initialized:', requestSigner.isInitialized());

      if (fingerprint && passphrase && !requestSigner.isInitialized()) {
        try {
          console.log('Auto-initializing request signer with auth data...');
          await requestSigner.initializeWorker(fingerprint, passphrase);
          console.log('Request signer auto-initialized successfully');
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
