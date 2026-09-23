<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { cryptoService } from '$lib/services/crypto';
  import { setTrustedServerKey } from '$lib/services/serverKeyTrust';
  import { refreshServerInfo } from '$lib/services/serverInfo';

  const dispatch = createEventDispatcher<{ trusted: void }>();

  let armor = '';
  let submitting = false;
  let error = '';

  async function submit() {
    error = '';
    const trimmed = armor.trim();
    if (!trimmed) return;

    submitting = true;
    try {
      // Validates the armor is parseable key material before it's ever
      // sent anywhere — a malformed paste fails here, never as a server
      // round trip.
      const fingerprint = await cryptoService.fingerprintFromArmor(trimmed);

      // Probe with the candidate fingerprint directly — nothing is
      // persisted until the server actually accepts it, so a rejection
      // just shows an error instead of clearing state and reloading.
      const response = await fetch('/api/server/info', {
        headers: { 'X-Syrinx-Server-Key-Fingerprint': fingerprint },
        signal: AbortSignal.timeout(8000),
      });
      if (!response.ok) {
        error = 'The server did not accept this key. Check it and try again.';
        return;
      }

      await setTrustedServerKey(trimmed);
      await refreshServerInfo();
      dispatch('trusted');
    } catch (err) {
      error = err instanceof Error ? err.message : 'That does not look like a valid public key.';
    } finally {
      submitting = false;
    }
  }
</script>

<div class="container">
  <div class="card">
    <label class="field">
      <span>Server public key</span>
      <textarea
        bind:value={armor}
        rows="10"
        spellcheck="false"
        placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----"
        disabled={submitting}
      ></textarea>
    </label>

    {#if error}
      <p class="error-box" role="alert">{error}</p>
    {/if}

    <button class="btn btn-primary" on:click={submit} disabled={submitting || !armor.trim()}>
      {submitting ? 'Checking…' : 'Continue'}
    </button>
  </div>
</div>

<style>
  .container {
    max-width: 640px;
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
    text-align: center;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    text-align: left;
    margin-bottom: 1rem;
  }

  .field span {
    color: var(--fg);
    font-weight: 600;
    font-size: 0.9rem;
  }

  .field textarea {
    font-family: ui-monospace, monospace;
    font-size: 0.85rem;
    padding: 0.75rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    resize: vertical;
    width: 36rem;
  }

  .error-box {
    background: rgba(244, 67, 54, 0.08);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    color: var(--error);
    font-size: 0.9rem;
    line-height: 1.5;
    margin: 0 0 1rem 0;
    text-align: left;
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
