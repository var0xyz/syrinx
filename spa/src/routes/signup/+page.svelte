<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { cryptoService } from '$lib/services/crypto';
  import { sessionStore } from '$lib/stores/session';
  import { getDeviceID } from '$lib/services/pwa';
  import PasswordStrength from '$lib/components/PasswordStrength.svelte';
  import UsernameChecker from '$lib/components/UsernameChecker.svelte';
  import ProgressBar from '$lib/components/ProgressBar.svelte';
  import { notificationStore } from '$lib/stores/notifications';

  let username = '';
  let email = '';
  let password = '';
  let loading = false;
  let currentStep = 0;
  let submitAttempted = false;
  let useBiometrics = true;
  let biometricEnabled = false;
  let biometricError = '';
  let biometricSupported = true;

  // Check if biometrics are supported on mount
  onMount(() => {
    if (!navigator.credentials || !navigator.credentials.create) {
      console.log('WebAuthn not supported on this device, showing password option only');
      biometricSupported = false;
      useBiometrics = false;
    }
  });


  async function enableBiometrics(event) {
    event.preventDefault();
    try {
      biometricError = '';

      if (!navigator.credentials || !navigator.credentials.create) {
        console.log('WebAuthn not supported on this device');
        biometricSupported = false;
        useBiometrics = false;
        return;
      }

      // Create a credential
      const deviceID = getDeviceID();
      if (!deviceID) {
        throw new Error('Device ID not found');
      }

      // Convert device ID string to ArrayBuffer
      const encoder = new TextEncoder();
      const userIdBuffer = encoder.encode(deviceID);

      const credential = await navigator.credentials.create({
        publicKey: {
          challenge: new Uint8Array(32), // Random challenge
          rp: {
            name: 'Syrinx',
            id: window.location.hostname
          },
          user: {
            id: userIdBuffer,
            name: username,
            displayName: username
          },
          pubKeyCredParams: [
            { type: 'public-key', alg: -7 }, // ES256
            { type: 'public-key', alg: -257 } // RS256
          ],
          authenticatorSelection: {
            authenticatorAttachment: 'platform',
            userVerification: 'required'
          },
          timeout: 60000
        }
      });

      if (credential && credential.id) {
        // Hash the credential ID to create a deterministic password
        const encoder = new TextEncoder();
        const data = encoder.encode(credential.id);
        const hashBuffer = await crypto.subtle.digest('SHA-256', data);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');

        // Use the hash as the password
        password = hashHex;
        biometricEnabled = true;

        notificationStore.success('Biometric authentication enabled');
      }
    } catch (error) {
      console.error('Biometric setup failed:', error);
      biometricError = error.message || 'Failed to enable biometric authentication';
      // Don't throw - just show the error and let user retry or switch to password
    }
  }


  async function onSubmit(e) {
    e.preventDefault();
    submitAttempted = true;

    // Validate based on authentication method
    if (useBiometrics && !biometricEnabled) {
      notificationStore.error('Please enable biometric authentication first');
      return;
    }

    if (!useBiometrics && !password) {
      notificationStore.error('Password is required');
      return;
    }

    loading = true;
    currentStep = 0;

    try {
      // Step 1: Generate encryption keys locally
      currentStep = 1;
      const keyPair = await cryptoService.generateKeyPair({
        userId: username, // Use username as userId for key generation
        email,
        password
      });

      // Step 2: Store keys in IndexedDB
      currentStep = 2;
      const { privateKeyRepository } = await import('$lib/repositories/privateKey');
      const { publicKeyRepository } = await import('$lib/repositories/publicKey');

      await Promise.all([
        privateKeyRepository.put(keyPair.fingerprint, keyPair.privateKey),
        publicKeyRepository.put(keyPair.fingerprint, keyPair.publicKey)
      ]);

      // Step 3: Sign the public key with private key
      currentStep = 3;
      const signature = await cryptoService.signMessage(
        keyPair.publicKey,
        keyPair.privateKey,
        password
      );

      // Step 4: Create user account with public key and signature
      currentStep = 4;
      const user = await authService.signup({
        username,
        publicKey: keyPair.publicKey,
        signature
      });

      // Step 5: Store user in both localStorage and IndexedDB
      currentStep = 5;
      await authService.saveUserToStorage(user);

      // Step 6: Store session data and set active key
      currentStep = 6;
      authService.setActiveKey(keyPair.fingerprint);

      // Store auth method in localStorage (persistent)
      authService.setAuthMethod(useBiometrics ? 'biometric' : 'password');

      // Store passphrase and fingerprint in session store (memory only)
      sessionStore.set('passphrase', password);
      sessionStore.set('fingerprint', keyPair.fingerprint);

      // Redirect to welcome page
      window.location.href = '/welcome';

    } catch (err) {
      loading = false;
      currentStep = 0;
      const errorMessage = 'Signup failed: ' + (err instanceof Error ? err.message : 'Unknown error');
      notificationStore.error(errorMessage);
      console.error(err);
    }
  }
</script>

<div class="container">
  <div class="card">
    <h2>Sign up</h2>
    <form on:submit|preventDefault={onSubmit}>
      <div class="row">
        <label for="username">Username</label>
        <input id="username" autocomplete="username" placeholder="All characters allowed" bind:value={username} required />
        <div class="help-text">
          <UsernameChecker {username} />
        </div>
      </div>

      <div class="row">
        <label for="email">Email Address</label>
        <input
          id="email"
          type="email"
          bind:value={email}
          placeholder="Optional"
          autocomplete="email"
        />
        <div class="help-text">
          Used for your encryption key identity, won't be verified
        </div>
      </div>

      <!-- Authentication Method Toggle -->
      {#if biometricSupported}
        <div class="row">
          <legend class="label">Authentication Method</legend>
          <div class="auth-method-toggle">
            <button type="button" class="toggle-option" class:active={useBiometrics} on:click={() => {
              useBiometrics = true;
              // Clear password field when switching to biometrics
              password = '';
            }}>
              <span class="icon">🫆</span>
              Biometrics
            </button>
            <button type="button" class="toggle-option" class:active={!useBiometrics} on:click={() => {
              useBiometrics = false;
              // Clear biometric state when switching to password
              biometricEnabled = false;
              biometricError = '';
            }}>
              <span class="icon">🔑</span>
              Password
            </button>
          </div>
        </div>
      {/if}

      {#if useBiometrics}
        <!-- Biometric Authentication -->
        <div class="row">
          {#if biometricError}
            <div class="error-message">
              {biometricError}
            </div>
          {/if}
          {#if !biometricEnabled}
            <button type="button" class="biometric-btn" on:click={enableBiometrics} disabled={loading}>
              Enable Biometric Authentication
            </button>
            <div class="help-text gap">
              Use your device's biometric authentication.
              <strong>Strongly recommended for enhanced security.</strong>
            </div>
          {:else}
            <div class="biometric-success">
              Biometric authentication enabled
            </div>
          {/if}
        </div>
      {:else}
        <!-- Password Authentication -->
        <div class="row">
          <label for="password">Password</label>
          <input id="password" type="password" placeholder="Min 16 characters, ideally 32" bind:value={password} required />
          <div class="help-text">
            <PasswordStrength {password} />
          </div>
        </div>
      {/if}
      {#if loading}
        <ProgressBar {currentStep} totalSteps={5} />
      {/if}
      <button disabled={loading} class="submit">
        {loading ? 'Creating account...' : 'Create account'}
      </button>
    </form>
  </div>
</div>

<style>
  .gap {
    margin-bottom: 1.8rem;
  }

  .auth-method-toggle {
    display: flex;
    gap: 0.5rem;
    margin-bottom: 1rem;
  }

  .auth-method-toggle button {
    color: var(--muted);
  }

  .auth-method-toggle button:hover {
    color: white;
  }

  .auth-method-toggle button.active {
    border-bottom: 1px solid white;
    color: white;
  }

  .toggle-option {
    flex: 1;
    padding: 0.75rem 1rem;
    background: none;
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.2s;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    font-size: 0.9rem;
    font-weight: 500;
    position: relative;
  }

  .toggle-option:hover {
    transform: translateY(-1px);
  }

  .toggle-option.active {
    color: white;
    transform: translateY(-1px);
  }

  .toggle-option .icon {
    font-size: 1.2rem;
  }

  .biometric-success {
    font-size: 0.8rem;
    padding: 1rem;
    color: #10b981;
    border-radius: 8px;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 500;
  }

  .error-message {
    color: #dc2626;
    padding: 0.75rem;
    border-radius: 4px;
    font-size: 0.9rem;
    margin-top: 0.5rem;
    font-weight: 500;
  }

  .help-text {
    font-size: 0.85rem;
    margin-top: 0.25rem;
  }

  form button.submit {
    margin-top: 1rem;
  }

  @media (max-width: 360px) {
    .auth-method-toggle button .icon {
      display: none;
    }
  }
</style>

