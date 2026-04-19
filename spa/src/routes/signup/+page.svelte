<script>
  import { onMount } from "svelte";
  import { authService } from "$lib/services/auth";

  onMount(() => {
    if (authService.isLoggedIn()) {
      goto('/reeds');
    }
  });
  import { cryptoService } from "$lib/services/crypto";
  import UsernameChecker from "$lib/components/UsernameChecker.svelte";
  import ProgressBar from "$lib/components/ProgressBar.svelte";
  import { notificationStore } from "$lib/stores/notifications";
  import { requestSigner } from "$lib/services/request-signer";
  import { serverConnection } from "$lib/services/serverConnection";
  import { goto } from "$app/navigation";

  let username = "";
  let email = "";
  let password = "";
  let loading = false;
  let currentStep = 0;
  let submitAttempted = false;

  // Generate a 32-character password with visible characters
  function generatePassword() {
    const chars =
      "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+-=[]{}|;:,.<>?";
    let result = "";
    for (let i = 0; i < 32; i++) {
      result += chars.charAt(Math.floor(Math.random() * chars.length));
    }
    return result;
  }

  async function onSubmit(e) {
    e.preventDefault();
    submitAttempted = true;

    loading = true;
    currentStep = 0;

    try {
      // Step 1: Generate encryption keys locally
      currentStep = 1;
      password = generatePassword();
      const keyPair = await cryptoService.generateKeyPair({
        name: username,
        email,
        password,
        comment: username,
      });
      authService.setPassphrase(password);

      // Step 2: Store keys in IndexedDB
      currentStep = 2;
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      const { publicKeyRepository } = await import(
        "$lib/repositories/publicKey"
      );

      await Promise.all([
        privateKeyRepository.put(keyPair.fingerprint, keyPair.privateKey),
        publicKeyRepository.put(keyPair.fingerprint, keyPair.publicKey),
      ]);

      // Step 3: Sign the public key with private key
      currentStep = 3;
      const signature = await cryptoService.signMessage(
        keyPair.publicKey,
        keyPair.privateKey,
        password,
      );

      // Step 4: Create user account with public key and signature
      currentStep = 4;
      const user = await authService.signup({
        username,
        publicKey: keyPair.publicKey,
        signature,
      });

      // Step 5: Store user in both localStorage and IndexedDB
      currentStep = 5;
      await authService.saveUserToStorage(user);

      // Step 6: Store session data and set active key
      currentStep = 6;
      authService.setActiveKey(keyPair.fingerprint);

      // Initialize the service worker with the new key so requests can be
      // signed immediately without a page reload. This also replaces any
      // stale key from a previous session that may still be loaded.
      await requestSigner.initializeWorker(keyPair.fingerprint, password);
      serverConnection.connect();

      // Redirect to welcome page
      goto("/welcome");
    } catch (err) {
      loading = false;
      currentStep = 0;
      const errorMessage =
        "Signup failed: " +
        (err instanceof Error ? err.message : "Unknown error");
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
        <label for="username">Name</label>
        <input
          id="username"
          autocomplete="username"
          bind:value={username}
          placeholder="Can be changed later"
          title="Robert'); DROP TABLE Students;--"
          maxlength="32"
          required
        />
        <div class="help-text">
          <UsernameChecker {username} />
        </div>
      </div>

      <div class="row">
        <label for="email">Email</label>
        <input
          id="email"
          type="email"
          bind:value={email}
          placeholder="Optional"
          autocomplete="email"
        />
        <div class="help-text">
          Used for your encryption key identity, won't be verified.
        </div>
      </div>

      {#if loading}
        <ProgressBar {currentStep} totalSteps={5} />
      {/if}
      <button disabled={loading} class="submit">
        {loading ? "Creating account..." : "Create account"}
      </button>
    </form>
  </div>
</div>

<style>
  .help-text {
    font-size: 0.85rem;
    min-height: 1rem;
  }

  form button.submit {
    margin-top: 1.5rem;
  }
</style>
