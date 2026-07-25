<script>
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import { authService } from "$lib/services/auth";
  import { cryptoService } from "$lib/services/crypto";
  import { buildNewUserIdentityPayload } from "$lib/services/signing";
  import UsernameChecker from "$lib/components/UsernameChecker.svelte";
  import ProgressBar from "$lib/components/ProgressBar.svelte";
  import { notificationStore } from "$lib/stores/notifications";
  import { requestSigner } from "$lib/services/request-signer";
  import { serverConnection } from "$lib/services/serverConnection";
  import {
    isSignupClosed,
    serverInfoLoading,
    signupMode,
  } from "$lib/services/serverInfo";
  import { get } from "svelte/store";

  let username = "";
  let email = "";
  let password = "";
  let loading = false;
  let currentStep = 0;
  let inviteToken = "";
  let inviteCheckFailed = false;
  let inviteChecking = false;
  let gateReady = false;

  // Invite links land on /signup?invite=TOKEN (skip preamble).
  $: inviteToken = ($page.url.searchParams.get("invite") || "").trim();

  onMount(async () => {
    if (authService.isLoggedIn()) {
      goto("/reeds");
      return;
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

    if (get(isSignupClosed)) {
      return;
    }

    const token = ($page.url.searchParams.get("invite") || "").trim();
    if (token) {
      inviteChecking = true;
      try {
        const { apiService } = await import("$lib/services/api");
        const result = await apiService.checkInvite(token);
        inviteCheckFailed = !result.valid;
      } catch (err) {
        console.error("invite check failed", err);
        inviteCheckFailed = false;
      } finally {
        inviteChecking = false;
      }
    }
  });

  function generatePassword() {
    const chars =
      "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+-=[]{}|;:,.<>?";
    let result = "";
    for (let i = 0; i < 32; i++) {
      result += chars.charAt(Math.floor(Math.random() * chars.length));
    }
    return result;
  }

  function friendlySignupError(raw) {
    const msg = typeof raw === "string" ? raw : "";
    if (msg.includes("Signups are closed")) {
      return "This server is not accepting new signups.";
    }
    if (msg.includes("Invite required")) {
      return "You need a valid invite link to join this server.";
    }
    if (msg.includes("Invalid or claimed invite")) {
      return "This invite is invalid or has already been used.";
    }
    return msg || "Unknown error";
  }

  async function cleanupFailedSignup(fingerprint) {
    try {
      localStorage.removeItem("keyPassphrase");
      localStorage.removeItem("keyFingerprint");
      localStorage.removeItem("userId");
    } catch {
      // ignore
    }
    if (!fingerprint) return;
    try {
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      await privateKeyRepository.deletePrivateKey(fingerprint);
    } catch (err) {
      console.error("Failed to clean up private key after signup error", err);
    }
  }

  async function onSubmit(e) {
    e.preventDefault();

    loading = true;
    currentStep = 0;
    let fingerprint = "";

    try {
      currentStep = 1;
      password = generatePassword();
      const keyPair = await cryptoService.generateKeyPair({
        name: username,
        email,
        password,
        comment: username,
      });
      fingerprint = keyPair.fingerprint;
      authService.setPassphrase(password);

      currentStep = 2;
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      const { publicKeyRepository } = await import(
        "$lib/repositories/publicKey"
      );
      const { apiService } = await import("$lib/services/api");

      await privateKeyRepository.put(keyPair.fingerprint, keyPair.privateKey);

      currentStep = 3;
      const signature = await cryptoService.signMessage(
        keyPair.publicKey,
        keyPair.privateKey,
        password,
      );

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

      currentStep = 5;
      const signupPayload = {
        username,
        publicKey: keyPair.publicKey,
        signature,
        userSignature,
      };
      if (inviteToken) {
        signupPayload.invite = inviteToken;
      }
      const user = await authService.signup(signupPayload);

      currentStep = 6;
      await authService.saveUserToStorage(user);
      authService.setActiveKey(keyPair.fingerprint);
      await requestSigner.initializeWorker(keyPair.fingerprint, password);

      currentStep = 7;
      const attestedKey = await apiService.getPublicKey(
        user.id,
        keyPair.fingerprint,
      );
      await publicKeyRepository.put(attestedKey);

      serverConnection.connect().then(() => serverConnection.syncRequest());

      goto("/welcome");
    } catch (err) {
      loading = false;
      currentStep = 0;
      await cleanupFailedSignup(fingerprint);
      const errorMessage =
        "Signup failed: " +
        friendlySignupError(err instanceof Error ? err.message : "");
      notificationStore.error(errorMessage);
      console.error(err);
    }
  }
</script>

<div class="container">
  <div class="card">
    <h2>Sign up</h2>

    {#if !gateReady || $serverInfoLoading || inviteChecking}
      <p class="gate-message">Checking signup availability…</p>
    {:else if $isSignupClosed}
      <p class="gate-message">
        This server is not accepting new signups.
      </p>
      <a href="/" class="back-link">Back to home</a>
    {:else if inviteCheckFailed}
      <p class="gate-message">
        This invite is invalid or has already been used.
      </p>
      <a href="/" class="back-link">Back to home</a>
    {:else}
      {#if $signupMode === "invite" && !inviteToken}
        <p class="invite-hint">
          This server requires an invite. If you are the first user you can
          continue; otherwise open the invite link you were given.
        </p>
      {/if}
      {#if inviteToken}
        <p class="invite-hint">Signing up with an invite link.</p>
      {/if}

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
    {/if}
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

  .gate-message,
  .invite-hint {
    color: var(--muted);
    line-height: 1.5;
    margin: 0 0 1rem 0;
  }

  .back-link {
    color: var(--primary);
  }
</style>
