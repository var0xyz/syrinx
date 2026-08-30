<script>
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import { authService } from "$lib/services/auth";
  import { cryptoService } from "$lib/services/crypto";
  import { buildNewUserIdentityPayload } from "$lib/services/signing";
  import { appendFingerprint } from "$lib/utils/identityRef";
  import { trimInvisibleChars } from "$lib/utils/text";
  import UsernameChecker from "$lib/components/UsernameChecker.svelte";
  import ProgressBar from "$lib/components/ProgressBar.svelte";
  import PreambleModal from "$lib/components/PreambleModal.svelte";
  import { notificationStore } from "$lib/stores/notifications";
  import { requestSigner } from "$lib/services/request-signer";
  import { serverConnection } from "$lib/services/serverConnection";
  import { requestPersistentStorage } from "$lib/services/pwa";
  import {
    isRecoveryMode,
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
  /** Every eligible signup must see the preamble — invited users too, not
   * just the open-home-page CTA (the old separate /preamble route only
   * covered the latter). Accepting is the only way to dismiss it. */
  let preambleAccepted = false;
  let inviteChecking = false;
  let gateReady = false;

  // Invite links: /signup?id=<id>#<secret> (secret stays in the fragment).
  $: inviteID = ($page.url.searchParams.get("id") || "").trim();

  $: usernameCheckFields =
    inviteID && inviteSecret ? { inviteID, inviteSecret } : {};

  onMount(async () => {
    if (authService.isLoggedIn()) {
      const user = await authService.getCurrentUser();
      if (user) {
        goto("/reeds");
        return;
      }
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

    if (get(isRecoveryMode) || get(isSignupClosed)) {
      return;
    }

    if (get(signupMode) === 'invite' && !inviteID) {
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

    if (!inviteCheckFailed) {
      await requestPersistentStorage();
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
    if (msg.includes("recovery mode")) {
      return "This server is rebuilding and is not accepting new signups.";
    }
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

  async function cleanupFailedSignup(keyId) {
    try {
      localStorage.removeItem("keyPassphrase");
      localStorage.removeItem("activeKeyId");
      localStorage.removeItem("userId");
    } catch {
      // ignore
    }
    if (!keyId) return;
    try {
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      await privateKeyRepository.deletePrivateKey(keyId);
    } catch (err) {
      console.error("Failed to clean up private key after signup error", err);
    }
  }

  async function onSubmit(e) {
    e.preventDefault();

    if (
      get(signupMode) === 'invite' &&
      (!inviteID || !inviteSecret || inviteCheckFailed)
    ) {
      notificationStore.error(
        'You need a valid invite link to join this server.'
      );
      return;
    }

    loading = true;
    currentStep = 0;
    let keyId = "";

    try {
      const { privateKeyRepository } = await import(
        "$lib/repositories/privateKey"
      );
      const { publicKeyRepository } = await import(
        "$lib/repositories/publicKey"
      );
      const { apiService, canonicalKeyId } = await import("$lib/services/api");

      currentStep = 1;
      const reserved = await apiService.getUserID();

      currentStep = 2;
      password = generatePassword();
      const serverId = localStorage.getItem('serverId') || '';
      const serverName = localStorage.getItem('serverName') || '';
      // This user's own canonical id, minted here for the first time.
      // Computed once; every use below passes it through as-is.
      const canonicalUserId = `${reserved.userID}@${serverId}`;
      const keyPair = await cryptoService.generateKeyPair({
        name: canonicalUserId,
        email,
        comment: serverName || undefined,
        password,
      });
      const newKeyId = appendFingerprint(canonicalUserId, keyPair.fingerprint);
      keyId = newKeyId;
      authService.setPassphrase(password);

      currentStep = 3;
      await privateKeyRepository.put(newKeyId, keyPair.privateKey);

      currentStep = 4;
      const signature = btoa(await cryptoService.signMessage(
        keyPair.publicKey,
        keyPair.privateKey,
        password,
      ));

      // Must match the server's trimInvisibleChars(username) exactly — it
      // rebuilds this same payload to verify userSignature, using the
      // sanitized username rather than what was typed.
      const trimmedUsername = trimInvisibleChars(username);
      const identityPayload = buildNewUserIdentityPayload(
        trimmedUsername,
        newKeyId,
      );
      const identitySigArmor = await cryptoService.signMessage(
        identityPayload,
        keyPair.privateKey,
        password,
      );
      const userSignature = btoa(identitySigArmor);

      currentStep = 5;
      const signupPayload = {
        username: trimmedUsername,
        publicKey: btoa(keyPair.publicKey),
        signature,
        userSignature,
        userID: reserved.userID,
        userIDSignature: reserved.signature,
        userIDFingerprint: reserved.fingerprint,
        ...(inviteID && inviteSecret ? { inviteID, inviteSecret } : {}),
      };
      const user = await authService.signup(signupPayload);

      // Request signing needs the session user id; getPublicKey is
      // authenticated. Cache the attested public key before the verified
      // user put — verifyUser resolves armor from IndexedDB.
      currentStep = 6;
      authService.setActiveKey(newKeyId);
      await requestSigner.initializeWorker(newKeyId, password);

      currentStep = 7;
      // getPublicKey takes an already-canonical GET /keys/{id} id — see
      // api.ts's canonicalKeyId.
      const attestedKey = await apiService.getPublicKey(
        canonicalKeyId(user.id, keyPair.fingerprint),
      );
      await publicKeyRepository.put(attestedKey);
      // A stale backup timestamp from a previous account on this browser
      // must not carry over — it would make Auth.svelte's welcome-page gate
      // think this brand new key has already been backed up.
      localStorage.removeItem('lastKeyBackupAt');
      await authService.saveUserToStorage(user);

      serverConnection.connect().then(() => serverConnection.syncRequest());

      window.location.href = '/welcome';
    } catch (err) {
      loading = false;
      currentStep = 0;
      await cleanupFailedSignup(keyId);
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
    {:else if $isRecoveryMode || $isSignupClosed}
      <p class="gate-message">
        This server is currently not accepting new signups.
      </p>
      <a href="/" class="back-link">Back to home</a>
    {:else if $signupMode === 'invite' && !inviteID}
      <p class="gate-message">
        You need a valid invite link to join this server.
      </p>
      <a href="/" class="back-link">Back to home</a>
    {:else if inviteCheckFailed}
      <p class="gate-message">
        This invite is invalid or has already been used.
      </p>
      <a href="/" class="back-link">Back to home</a>
    {:else if !preambleAccepted}
      <PreambleModal open on:accept={() => (preambleAccepted = true)} />
    {:else}
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
            <UsernameChecker {username} extraFormFields={usernameCheckFields} />
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
          <ProgressBar {currentStep} totalSteps={7} />
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

  .gate-message {
    color: var(--muted);
    line-height: 1.5;
    margin: 0 0 1rem 0;
  }

  .back-link {
    color: var(--primary);
  }
</style>
