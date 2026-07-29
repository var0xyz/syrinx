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
  let inviteID = "";
  let inviteSecret = "";
  let inviteCheckFailed = false;
  let inviteChecking = false;
  let gateReady = false;

  // Invite links: /signup?invite=<id>#<secret> (secret stays in the fragment).
  $: inviteID = ($page.url.searchParams.get("invite") || "").trim();

  onMount(async () => {
    if (authService.isLoggedIn()) {
      goto("/reeds");
      return;
    }

    // Fragment is not always on $page.url in SSR; read from location.
    inviteSecret = (typeof window !== "undefined"
      ? window.location.hash.replace(/^#/, "")
      : ""
    ).trim();

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

    if (inviteID && inviteSecret) {
      inviteChecking = true;
      try {
        const { apiService } = await import("$lib/services/api");
        const result = await apiService.checkInvite(inviteID, inviteSecret);
        inviteCheckFailed = !result.valid;
      } catch (err) {
        console.error("invite check failed", err);
        inviteCheckFailed = false;
      } finally {
        inviteChecking = false;
      }
    } else if (inviteID && !inviteSecret) {
      // Query id without fragment secret — treat as broken link.
      inviteCheckFailed = true;
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
        ...(inviteID && inviteSecret
          ? { inviteID, inviteSecret }
          : {}),
      };
      const user = await authService.signup(signupPayload);

      // Request signing needs the session user id; getPublicKey is
      // authenticated. Cache the attested public key before the verified
      // user put — verifyUser resolves armor from IndexedDB.
      currentStep = 6;
      authService.setActiveKey(keyPair.fingerprint);
      await requestSigner.initializeWorker(keyPair.fingerprint, password);

      currentStep = 7;
      const attestedKey = await apiService.getPublicKey(
        user.id,
        keyPair.fingerprint,
      );
      await publicKeyRepository.put(attestedKey);
      await authService.saveUserToStorage(user);
      if (user.invitedBy?.id) {
        const { followingRepository } = await import("$lib/repositories/following");
        await followingRepository.recordLocalFollow(user.invitedBy.id);
      }

      serverConnection.connect().then(() => serverConnection.syncRequest());

      window.location.href = '/welcome';
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
      {#if $signupMode === "invite" && !inviteID}
        <p class="invite-hint">
          This server requires an invite. If you are the first user you can
          continue; otherwise open the invite link you were given.
        </p>
      {/if}
      {#if inviteID && inviteSecret}
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
