<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { get } from 'svelte/store';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { requestSigner } from '$lib/services/request-signer';
  import {
    assertBackupIdentity,
    decryptBackupFile,
    extractProfile,
    isFullBackupFilename,
    isIdentityBackupFilename,
    isIdentityBackupPayload,
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

  type ImportMode = 'backup' | 'identity';

  let mode: ImportMode = 'backup';
  let files: FileList | null = null;
  let password = '';
  let restoring = false;
  let error = '';
  let importSucceeded = false;

  $: file = files?.[0] ?? null;
  $: canRestore = file !== null && password.length > 0;
  $: resumeImport = mode === 'backup' && isImportInProgress();
  $: fileAccept = mode === 'backup' ? '.sxb.gpg' : '.sxi.gpg';
  $: fileLabel = mode === 'backup' ? 'Backup file' : 'Identity file';
  $: passwordLabel = mode === 'backup' ? 'Backup password' : 'Export password';
  $: submitLabel = mode === 'backup'
    ? (restoring ? 'Importing...' : 'Import backup')
    : (restoring ? 'Restoring...' : 'Restore with keys');

  const DEVICE_TAKEOVER_CONFIRM =
    'Continuing will log out any other devices that are signed in with this account. Continue?';

  onMount(async () => {
    if (await redirectForRestoreState()) return;
    if (isImportComplete() && !isRecoveryInProgress()) {
      importSucceeded = true;
    }
  });

  function switchMode(next: ImportMode) {
    if (mode === next) return;
    mode = next;
    files = null;
    password = '';
    error = '';
  }

  function validateFileForMode(name: string): void {
    if (mode === 'backup') {
      if (!isFullBackupFilename(name)) {
        if (isIdentityBackupFilename(name)) {
          throw new Error(
            'That is an identity export. Switch to “I only have my keys” to restore it.'
          );
        }
        throw new Error('Please select a full backup file (syrinx-….sxb.gpg).');
      }
      return;
    }
    if (!isIdentityBackupFilename(name)) {
      if (isFullBackupFilename(name)) {
        throw new Error(
          'That is a full backup. Switch to “Full backup” to restore it.'
        );
      }
      throw new Error('Please select an identity export (syrinx-….sxi.gpg).');
    }
  }

  async function handleIdentityRestore() {
    if (!file) return;

    const confirmed = confirm(DEVICE_TAKEOVER_CONFIRM);
    if (!confirmed) return;

    restoring = true;
    error = '';

    try {
      validateFileForMode(file.name);
      const backup = await decryptBackupFile(file, password);
      if (!isIdentityBackupPayload(backup)) {
        throw new Error('Invalid identity export: profile must not be included.');
      }
      await restoreFromIdentityBackup(backup);
      window.location.assign('/reeds');
    } catch (e) {
      error = e instanceof Error ? e.message : 'Restore failed. Please try again.';
    } finally {
      restoring = false;
    }
  }

  async function handleBackupRestore() {
    if (!file) return;

    const confirmed = confirm(DEVICE_TAKEOVER_CONFIRM);
    if (!confirmed) return;

    restoring = true;
    error = '';
    importSucceeded = false;
    startImportRun();

    try {
      validateFileForMode(file.name);
      const backup = await decryptBackupFile(file, password);

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

  async function handleRestore() {
    if (mode === 'identity') {
      await handleIdentityRestore();
    } else {
      await handleBackupRestore();
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
          <a href="/account" class="btn btn-secondary">Go to profile</a>
        </div>
      </div>
    {:else}
      <h2>Restore account</h2>

      <div class="mode-tabs" role="tablist" aria-label="Restore mode">
        <button
          type="button"
          role="tab"
          class="mode-tab"
          class:active={mode === 'backup'}
          aria-selected={mode === 'backup'}
          on:click={() => switchMode('backup')}
        >
          Full backup
        </button>
        <button
          type="button"
          role="tab"
          class="mode-tab"
          class:active={mode === 'identity'}
          aria-selected={mode === 'identity'}
          on:click={() => switchMode('identity')}
        >
          I only have my keys
        </button>
      </div>

      {#if mode === 'backup'}
        <p class="subtitle">
          Restore from an encrypted full export
          (<code>syrinx-….sxb.gpg</code>). You will need the backup password.
        </p>
      {:else}
        <p class="subtitle">
          Restore from an identity export created with Backup Keys
          (<code>syrinx-….sxi.gpg</code>). Your profile and reeds will be
          fetched from the server and network.
        </p>
      {/if}

      {#if resumeImport}
        <div class="info-box" role="status">
          An import was interrupted. Select your backup again to continue.
        </div>
      {/if}

      <div class="field">
        <label for="backup-file">{fileLabel}</label>
        <input
          id="backup-file"
          type="file"
          accept={fileAccept}
          bind:files
        />
      </div>

      <div class="field">
        <label for="import-password">{passwordLabel}</label>
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
          {submitLabel}
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

  .mode-tabs {
    display: flex;
    gap: 0.25rem;
    padding: 0.25rem;
    background: var(--input-bg);
    border-radius: 8px;
  }

  .mode-tab {
    flex: 1;
    padding: 0.5rem 0.75rem;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--muted);
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s ease, color 0.15s ease;
  }

  .mode-tab.active {
    background: var(--surface);
    color: var(--fg);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
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
