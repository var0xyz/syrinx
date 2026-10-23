<script lang="ts">
  import { invalidateAll } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { requestSigner } from '$lib/services/request-signer';
  import { buildUserIdentityPayload } from '$lib/services/signing';
  import { lostUsername } from '$lib/services/usernameCollision';
  import { userRepository } from '$lib/repositories/user';
  import { trimInvisibleChars } from '$lib/utils/text';
  import UsernameChecker from '$lib/components/UsernameChecker.svelte';

  /** The username an older claim took. */
  export let username: string;

  let next = '';
  let saving = false;
  let error = '';

  async function save() {
    error = '';
    // Signed as sent: trimmed exactly like the server does.
    const name = trimInvisibleChars(next);
    if (name === '') return;
    if (name.length > 32) {
      error = 'Username cannot exceed 32 characters';
      return;
    }
    const keyId = authService.getActiveKeyId();
    if (!keyId) {
      error = 'Session expired. Please sign in again.';
      return;
    }
    saving = true;
    try {
      const bio = (await authService.getCurrentUser())?.bio ?? '';
      const userSignature = await requestSigner.sign(buildUserIdentityPayload(name, keyId, bio));
      const updated = await apiService.updateUser({ username: name, bio, userSignature });
      await authService.saveUserToStorage(updated);
      await userRepository.put(updated);
      lostUsername.set(null);
      await invalidateAll();
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to save. Please try again.';
    } finally {
      saving = false;
    }
  }
</script>

<div class="container">
  <form class="card" on:submit|preventDefault={save}>
    <h2>Pick a new username</h2>
    <p>
      This server was rebuilt, and someone who held <strong>@{username}</strong> on it before
      came back for it. Your account and everything in it are yours; only the name changes.
    </p>

    <label class="field">
      <span>New username</span>
      <input bind:value={next} placeholder="Enter username" disabled={saving} />
    </label>
    <div class="help-text">
      <UsernameChecker username={next} authenticated />
    </div>

    {#if error}
      <p class="error-box" role="alert">{error}</p>
    {/if}

    <button class="btn btn-primary" type="submit" disabled={saving || !next.trim()}>
      {saving ? 'Saving…' : 'Continue'}
    </button>
  </form>
</div>

<style>
  .container {
    max-width: 41rem;
    margin: 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    width: 100%;
  }

  h2 {
    margin: 0 0 1rem;
  }

  p {
    color: var(--muted);
    line-height: 1.5;
    margin: 0 0 1.5rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .field span {
    color: var(--fg);
    font-weight: 600;
    font-size: 0.9rem;
  }

  .field input {
    padding: 0.75rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
  }

  .help-text {
    margin: 0.5rem 0 1rem;
    min-height: 1.25rem;
  }

  .error-box {
    background: rgba(244, 67, 54, 0.08);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    color: var(--error);
    font-size: 0.9rem;
    margin: 0 0 1rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    font-weight: 600;
    border: none;
    cursor: pointer;
    width: 100%;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }
</style>
