<script>
  import { createEventDispatcher, onMount } from 'svelte';
  import { authService } from '../services/auth';
  import { authenticateWithBiometric, isBiometricSupported } from '../services/pwa';

  export let isOpen = false;
  export let error = '';
  export let loading = false;

  const dispatch = createEventDispatcher();

  let passphrase = '';
  let showError = false;
  let authMethod = 'password';
  let biometricSupported = false;
  let biometricError = '';

  // Watch for error changes to show with delay
  $: if (error) {
    showError = true;
    setTimeout(() => {
      showError = false;
    }, 3000);
  }

  onMount(() => {
    // Get the authentication method from auth service (localStorage)
    const storedMethod = authService.getAuthMethod();
    if (storedMethod) {
      authMethod = storedMethod;
    }

    // Check if biometrics are supported
    checkBiometricSupport();
  });

  async function checkBiometricSupport() {
    biometricSupported = await isBiometricSupported();
  }

  async function handleBiometricAuth() {
    try {
      biometricError = '';
      const success = await authenticateWithBiometric();

      if (success) {
        // For biometric auth, we need to get the passphrase from the stored credential
        // This is the same logic used in signup - hash the credential ID
        const credentialId = localStorage.getItem('biometric_credential_id');
        if (credentialId) {
          const encoder = new TextEncoder();
          const data = encoder.encode(credentialId);
          const hashBuffer = await crypto.subtle.digest('SHA-256', data);
          const hashArray = Array.from(new Uint8Array(hashBuffer));
          const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');

          dispatch('passphrase-submit', { passphrase: hashHex, method: 'biometric' });
        } else {
          biometricError = 'Biometric credential not found. Please sign up again.';
        }
      } else {
        biometricError = 'Biometric authentication failed. Please try again.';
      }
    } catch (err) {
      console.error('Biometric authentication error:', err);
      biometricError = 'Biometric authentication failed. Please try again.';
    }
  }

  function handlePasswordSubmit() {
    if (passphrase.trim()) {
      dispatch('passphrase-submit', { passphrase: passphrase.trim(), method: 'password' });
      passphrase = '';
    }
  }

  function handleKeydown(event) {
    if (event.key === 'Enter' && authMethod === 'password') {
      handlePasswordSubmit();
    }
  }
</script>

{#if isOpen}
  <div class="modal-overlay">
    <div class="modal">
      <div class="modal-header">
        <h3>Enter Passphrase</h3>
      </div>

      <div class="modal-body">
        <p>Please authenticate to unlock your encryption key:</p>

        {#if authMethod === 'biometric' && biometricSupported}
          <!-- Biometric Authentication -->
          <div class="auth-method-section">
            <button
              type="button"
              class="biometric-btn"
              on:click={handleBiometricAuth}
              disabled={loading}
            >
              <span class="icon">🫆</span>
              Use Biometric Authentication
            </button>

            {#if biometricError}
              <div class="error-message">
                {biometricError}
              </div>
            {/if}
          </div>
        {:else}
          <!-- Password Authentication -->
          <div class="auth-method-section">
            <div class="input-group">
              <input
                type="password"
                bind:value={passphrase}
                on:keydown={handleKeydown}
                placeholder="Enter your passphrase"
                disabled={loading}
              />
            </div>
          </div>
        {/if}

        {#if showError && error}
          <div class="error-message">
            {error}
          </div>
        {/if}
      </div>

      <div class="modal-footer">
        {#if authMethod === 'password'}
          <button
            type="button"
            on:click={handlePasswordSubmit}
            disabled={loading || !passphrase.trim()}
            class="primary"
          >
            {loading ? 'Unlocking...' : 'Unlock Key'}
          </button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.8);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }

  .modal {
    background: var(--bg-primary);
    border-radius: 8px;
    padding: 0;
    max-width: 400px;
    width: 90%;
    box-shadow: 0 10px 25px rgba(0, 0, 0, 0.3);
  }

  .modal-header {
    padding: 1.5rem 1.5rem 0 1.5rem;
  }

  .modal-header h3 {
    margin: 0;
    color: var(--text-primary);
    font-size: 1.25rem;
  }

  .modal-body {
    padding: 1rem 1.5rem;
  }

  .modal-body p {
    margin: 0 0 1rem 0;
    color: var(--text-secondary);
  }

  .input-group {
    margin-bottom: 1rem;
  }

  .input-group input {
    width: 100%;
    padding: 0.75rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    font-size: 1rem;
  }

  .input-group input:focus {
    outline: none;
    border-color: var(--accent-color);
  }

  .input-group input:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .error-message {
    background: var(--error-bg);
    color: var(--error-text);
    padding: 0.75rem;
    border-radius: 4px;
    font-size: 0.9rem;
    margin-top: 0.5rem;
  }

  .modal-footer {
    padding: 0 1.5rem 1.5rem 1.5rem;
    display: flex;
    justify-content: flex-end;
  }

  .modal-footer button {
    padding: 0.75rem 1.5rem;
    border: none;
    border-radius: 4px;
    font-size: 1rem;
    cursor: pointer;
    transition: background-color 0.2s;
  }

  .modal-footer button.primary {
    background: var(--accent-color);
    color: white;
  }

  .modal-footer button.primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  .modal-footer button:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .auth-method-section {
    margin-bottom: 1rem;
  }

  .biometric-btn {
    width: 100%;
    padding: 1rem;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: 8px;
    font-size: 1rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.2s;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
  }

  .biometric-btn:hover:not(:disabled) {
    background: var(--accent-hover);
    transform: translateY(-1px);
  }

  .biometric-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
    transform: none;
  }

  .biometric-btn .icon {
    font-size: 1.2rem;
  }
</style>
