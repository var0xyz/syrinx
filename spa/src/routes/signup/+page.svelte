<script>
  import { onMount } from "svelte";
  import { authService } from "$lib/services/auth";

  onMount(() => {
    if (authService.isLoggedIn()) {
      goto('/reeds');
    }
  });
  import { cryptoService } from "$lib/services/crypto";
  import { buildNewUserIdentityPayload } from "$lib/services/signing";
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

      // Step 2: Store the private key locally. The public key is cached
      // only after signup, once the server has countersigned it.
      currentStep = 2;
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      const { publicKeyRepository } = await import(
        "$lib/repositories/publicKey"
      );
      const { apiService } = await import("$lib/services/api");

      await privateKeyRepository.put(keyPair.fingerprint, keyPair.privateKey);

      // Step 3: Sign the public key with private key
      currentStep = 3;
      const signature = await cryptoService.signMessage(
        keyPair.publicKey,
        keyPair.privateKey,
        password,
      );

      // Step 4: Build and sign the user identity payload. This is the
      // user-authored half of the signed identity record the server
      // will countersign. At signup avatarURL and bio are always empty
      // — a user cannot have set them before their account exists —
      // and the signed bytes must match the ones the server rebuilds
      // via identity.buildNewUserIdentityPayload. The signature travels
      // as base64(armored PGP) to survive form-encoding.
      currentStep = 4;
      const identityPayload = buildNewUserIdentityPayload(
        username,
        keyPair.fingerprint,
      );
      const identitySigArmor = await cryptoService.signMessage(
        identityPayload,
        keyPair.privateKey,
        password,
      );
      const userSignature = btoa(identitySigArmor);

      // Step 5: Create user account with public key + both signatures
      currentStep = 5;
      const user = await authService.signup({
        username,
        publicKey: keyPair.publicKey,
        signature,
        userSignature,
      });

      // Step 6: Persist session first so authenticated GETs (attested
      // public key, server signing keys for verify) can be signed.
      currentStep = 6;
      await authService.saveUserToStorage(user);
      authService.setActiveKey(keyPair.fingerprint);
      await requestSigner.initializeWorker(keyPair.fingerprint, password);

      // Step 7: Cache the server-attested public key
      currentStep = 7;
      const attestedKey = await apiService.getPublicKey(user.id, keyPair.fingerprint);
      await publicKeyRepository.put(attestedKey);

      serverConnection.connect().then(() => serverConnection.syncRequest());

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
        <ProgressBar {currentStep} totalSteps={6} />
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
