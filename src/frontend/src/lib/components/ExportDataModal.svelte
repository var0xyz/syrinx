<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import PasswordStrength from '$lib/components/PasswordStrength.svelte';

  export let open: boolean = false;

  const dispatch = createEventDispatcher();

  let password = '';
  let confirmPassword = '';

  $: passwordsMatch = password.length > 0 && password === confirmPassword;
  $: canConfirm = password.length > 0 && passwordsMatch;

  function confirm() {
    if (!canConfirm) return;
    dispatch('confirm', password);
    password = '';
    confirmPassword = '';
  }

  function cancel() {
    password = '';
    confirmPassword = '';
    dispatch('cancel');
  }
</script>

{#if open}
  <div class="overlay" on:click={cancel} role="presentation"></div>
  <div class="modal" role="dialog" aria-modal="true">
    <h3>🔒 Encrypt your backup</h3>
    <p>Your backup will be encrypted with OpenPGP using the password you provide. You will need this password to restore from the backup.</p>
    <p>Use a strong, unique password and store it somewhere safe — without it, the backup cannot be recovered.</p>

    <div class="field">
      <label for="backup-password">Password</label>
      <input
        id="backup-password"
        type="password"
        bind:value={password}
        placeholder="Enter a strong password"
        autocomplete="new-password"
      />
      <PasswordStrength {password} />
    </div>

    <div class="field">
      <label for="backup-password-confirm">Confirm password</label>
      <input
        id="backup-password-confirm"
        type="password"
        bind:value={confirmPassword}
        placeholder="Repeat your password"
        autocomplete="new-password"
      />
      {#if confirmPassword.length > 0 && !passwordsMatch}
        <span class="mismatch">Passwords do not match</span>
      {/if}
    </div>

    <div class="actions">
      <button class="btn btn-secondary" on:click={cancel}>Cancel</button>
      <button class="btn btn-primary" on:click={confirm} disabled={!canConfirm}>
        Export
      </button>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 2000;
  }

  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 2001;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    width: min(420px, calc(100vw - 2rem));
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  p {
    margin: 0;
    font-size: 0.9rem;
    line-height: 1.5;
    color: var(--fg);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .field label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--fg);
  }

  .field input {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
  }

  .field input:focus {
    outline: none;
    border-color: var(--primary);
  }

  .mismatch {
    font-size: 0.8rem;
    color: var(--error);
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    transition: all 0.2s ease;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    transform: none;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:not(:disabled):hover { opacity: 0.9; }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover { background: var(--border); }
</style>
