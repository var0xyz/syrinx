<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { get } from 'svelte/store';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { ensureDeviceId } from '$lib/services/deviceId';
  import { requestSigner } from '$lib/services/request-signer';
  import {
    assertBackupIdentity,
    decryptBackupFile,
    extractProfile,
    isBackupFilename,
    isFullBackupFilename,
    isIdentityBackupFilename,
    writeBackup,
  } from '$lib/services/backupRestore';
  import { restoreFromIdentityBackup } from '$lib/services/accountRecovery';
  import {
    completeImportRun,
    isImportComplete,
    isImportInProgress,
    startImportRun,
  } from '$lib/services/importRun';
  import {
    clearRecoveryRun,
    startRecoveryRun,
    isRecoveryInProgress,
    resumeRecoveryRun,
  } from '$lib/services/recoveryRun';
  import { ensureRecoveryProgress } from '$lib/services/recoveryProgress';
  import { redirectForRestoreState } from '$lib/services/restoreFlow';
  import { isRecoveryMode, serverInfoLoading } from '$lib/services/serverInfo';

  let files: FileList | null = null;
  let password = '';
  let restoring = false;
  let error = '';
  let importSucceeded = false;

  $: file = files?.[0] ?? null;
  $: canRestore = file !== null && password.length > 0;
  $: resumeImport = isImportInProgress();

  onMount(() => {
    if (redirectForRestoreState()) return;
    if (isImportComplete() && !isRecoveryInProgress()) {
      importSucceeded = true;
    }
  });

  async function handleRestore() {
    if (!file) return;

    const confirmed = confirm(
      'Importing this backup will log out any other devices that are signed in with this account. Continue?'
    );
    if (!confirmed) return;

    restoring = true;
    error = '';
    importSucceeded = false;
    startImportRun();

    try {
      if (!isBackupFilename(file.name)) {
        throw new Error(
          'Please select a Syrinx backup file (syrinx-….sxb.gpg or syrinx-….sxi.gpg).'
        );
      }

      const backup = await decryptBackupFile(file, password);
      const identityImport = isIdentityBackupFilename(file.name);

      if (identityImport) {
        await restoreFromIdentityBackup(backup);
        completeImportRun();
        importSucceeded = true;
        return;
      }

      if (!isFullBackupFilename(file.name)) {
        throw new Error('Please select a Syrinx backup file (syrinx-….sxb.gpg or syrinx-….sxi.gpg).');
      }

      assertBackupIdentity(backup);
      const profile = extractProfile(backup);

      const probe = await apiService.probeUserStatus(profile);
      if (probe.httpStatus === 400) {
        throw new Error(probe.error || 'This backup was rejected by the server.');
      }
      if (probe.error) {
        throw new Error(probe.error);
      }

      const recoveryMode = !get(serverInfoLoading) && get(isRecoveryMode);
      const localRecovery = isRecoveryInProgress();

      if (probe.httpStatus === 200 && probe.status === 'complete') {
        if (localRecovery) {
          clearRecoveryRun();
        }
        await writeBackup(backup);
        const fingerprint = authService.getActiveKeyFingerprint();
        const passphrase = authService.getPassphrase();
        if (!fingerprint || !passphrase) {
          throw new Error('Restored backup is missing key material for device binding.');
        }
        await requestSigner.initializeWorker(fingerprint, passphrase);
        await apiService.bindDevice();
        clearRecoveryRun();
        completeImportRun();
        importSucceeded = true;
        return;
      }

      const needsRecovery =
        (probe.httpStatus === 404 && probe.status === 'unknown' && recoveryMode) ||
        (probe.httpStatus === 409 && probe.status === 'ongoing');

      if (probe.httpStatus === 404 && probe.status === 'unknown' && !recoveryMode) {
        throw new Error(
          'This account is not recognized on this server. Nothing was written.'
        );
      }

      if (needsRecovery) {
        await writeBackup(backup);
        if (localRecovery) {
          resumeRecoveryRun();
        } else {
          startRecoveryRun();
        }
        await ensureRecoveryProgress();
        completeImportRun();
        goto('/recovery');
        return;
      }

      throw new Error(
        `Unexpected account status (HTTP ${probe.httpStatus}). Nothing was written.`
      );
    } catch (e) {
      error = e instanceof Error ? e.message : 'Restore failed. Please try again.';
    } finally {
      restoring = false;
    }
  }
</script>

<div class="container">
  <div class="card">
    {#if importSucceeded}
      <div class="success-view">
        <div class="success-icon">✓</div>
        <h2>Import complete</h2>
        <p>Your backup has been restored. You can now access your account.</p>
        <div class="success-actions">
          <a href="/reeds" class="btn btn-primary">Go to reeds</a>
          <a href="/profile" class="btn btn-secondary">Go to profile</a>
        </div>
      </div>
    {:else}
      <h2>Import backup</h2>
      <p class="subtitle">
        Restore your account from an encrypted Syrinx backup
        (<code>syrinx-….sxb.gpg</code> full export or <code>syrinx-….sxi.gpg</code> identity).
        You will need the backup password.
      </p>

      {#if resumeImport}
        <div class="info-box" role="status">
          An import was interrupted. Select your backup again to continue.
        </div>
      {/if}

      <div class="field">
        <label for="backup-file">Backup file</label>
        <input
          id="backup-file"
          type="file"
          accept=".sxb.gpg,.sxi.gpg"
          bind:files
        />
      </div>

      <div class="field">
        <label for="import-password">Backup password</label>
        <input
          id="import-password"
          type="password"
          bind:value={password}
          placeholder="Password used when exporting"
          autocomplete="current-password"
        />
      </div>

      {#if error}
        <div class="error-box">{error}</div>
      {/if}

      <div class="actions">
        <a href="/" class="btn btn-secondary">Cancel</a>
        <button
          class="btn btn-primary"
          on:click={handleRestore}
          disabled={!canRestore || restoring}
        >
          {restoring ? 'Importing...' : 'Import'}
        </button>
      </div>
    {/if}
  </div>
</div>

<style>
  .container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    width: min(440px, 100%);
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  h2 {
    margin: 0;
    color: var(--fg);
    font-size: 1.4rem;
  }

  .subtitle {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  code {
    font-family: monospace;
    font-size: 0.85em;
    background: var(--input-bg);
    padding: 0.1em 0.3em;
    border-radius: 3px;
  }

  .info-box {
    background: rgba(33, 150, 243, 0.08);
    border: 1px solid rgba(33, 150, 243, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    font-size: 0.9rem;
    color: var(--fg);
    line-height: 1.5;
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

  .field input[type="password"] {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
  }

  .field input[type="password"]:focus {
    outline: none;
    border-color: var(--primary);
  }

  .field input[type="file"] {
    font-size: 0.9rem;
    color: var(--fg);
  }

  .error-box {
    background: rgba(244, 67, 54, 0.08);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    font-size: 0.9rem;
    color: var(--error);
    line-height: 1.5;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.55rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    text-decoration: none;
    transition: all 0.2s ease;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
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

  .btn-secondary:hover { background: var(--input-bg); }

  .success-view {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
    text-align: center;
    padding: 1rem 0;
  }

  .success-icon {
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: rgba(76, 175, 80, 0.15);
    color: #4caf50;
    font-size: 1.75rem;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .success-view h2 {
    margin: 0;
  }

  .success-view p {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .success-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 0.5rem;
    flex-wrap: wrap;
    justify-content: center;
  }
</style>
