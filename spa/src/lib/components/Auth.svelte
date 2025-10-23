<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { requestSigner } from '../services/request-signer';
  import { authService } from '../services/auth';
  import PassphraseModal from './PassphraseModal.svelte';

  export let children;

  let isChecking = true;
  let showPassphraseModal = false;
  let passphraseError = '';
  let passphraseLoading = false;
  let authError = '';

  // Computed property for error display
  $: displayError = authError || passphraseError;

  onMount(async () => {
    await checkAuthentication();

    // Set up global error handler for authentication errors
    window.addEventListener('unhandledrejection', (event) => {
      console.error('Authentication error:', event.reason?.message);
      if (event.reason?.message?.includes('Authentication required') ||
          event.reason?.message?.includes('Authentication failed')) {
        authError = 'Authentication required. Please enter your passphrase.';
        showPassphraseModal = true;
        event.preventDefault();
      }
    });
  });

  async function checkAuthentication() {
    try {
      // Check if user has required data
      const user = await authService.getCurrentUser();

      if (!user) {
        // No user ID or active key, redirect to signup
        goto('/signup');
        return;
      }

      // Don't check request signer initialization here
      // Let the first API call trigger the passphrase prompt if needed

      isChecking = false;
    } catch (error) {
      console.error('Authentication check failed:', error);
      goto('/signup');
    }
  }

  async function handlePassphraseSubmit(event) {
    const { passphrase } = event.detail;
    passphraseLoading = true;
    passphraseError = '';
    authError = '';

    try {
      const fingerprint = authService.getActiveKeyFingerprint();
      if (!fingerprint) {
        throw new Error('No active key found');
      }

      await requestSigner.initializeWorker(fingerprint, passphrase);

      // Success - hide modal and show content
      showPassphraseModal = false;
      passphraseLoading = false;
    } catch (error) {
      console.error('Failed to initialize with passphrase:', error);
      passphraseError = 'Invalid passphrase. Please try again.';
      passphraseLoading = false;
    }
  }

  async function handleLogout() {
    try {
      await authService.logout();
      await requestSigner.clearSession();
      goto('/signup');
    } catch (error) {
      console.error('Logout failed:', error);
    }
  }
</script>

{#if isChecking}
  <div class="loading">
    <div class="spinner"></div>
    <p>Checking authentication...</p>
  </div>
{:else if showPassphraseModal}
  <PassphraseModal
    bind:isOpen={showPassphraseModal}
    bind:error={displayError}
    bind:loading={passphraseLoading}
    on:passphrase-submit={handlePassphraseSubmit}
  />
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
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }

  .auth-content {
    flex: 1;
  }
</style>
