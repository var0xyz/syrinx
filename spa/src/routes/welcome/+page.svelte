<script>
  import { page } from '$app/stores';
  import { notificationStore } from '$lib/stores/notifications';
  import { compressBackupPayload, encryptAndSaveBackup, buildKeyBackupPayload } from '$lib/services/backupRestore';
  import { recordBackupEvent } from '$lib/services/backupMetrics';
  import ExportDataModal from '$lib/components/ExportDataModal.svelte';
  import Auth from '$lib/components/Auth.svelte';

  $: user = $page.data?.user;

  let backingUp = false;
  let showBackupModal = false;
  /** Mandatory before continuing — Auth.svelte redirects back here on every
   * authenticated page load until lastKeyBackupAt is set. Seeded from
   * localStorage so a user who already backed up (e.g. navigated back to
   * /welcome manually) isn't blocked again. */
  let backedUp = typeof localStorage !== 'undefined' && !!localStorage.getItem('lastKeyBackupAt');

  async function backupKeys(password) {
    backingUp = true;
    try {
      const payload = await buildKeyBackupPayload();
      const compressedData = await compressBackupPayload(payload);
      const filename = `syrinx-${user?.id ?? 'identity'}-${payload.timestamp}.sxi.gpg`;
      const saved = await encryptAndSaveBackup(compressedData, password, filename, 'identity');

      if (saved) {
        notificationStore.success('Keys backed up successfully');
        localStorage.setItem('lastKeyBackupAt', String(payload.timestamp));
        backedUp = true;
        void recordBackupEvent('identity');
      }
    } catch (error) {
      console.error('Error backing up keys:', error);
      notificationStore.error(
        error instanceof Error ? error.message : 'Failed to back up keys'
      );
    } finally {
      backingUp = false;
    }
  }
</script>

<Auth>
  <div class="container">
    <div class="card">
      <div class="welcome-header">
        <h1>🎉 Welcome!</h1>
        <p class="subtitle">Your account has been successfully created.</p>
      </div>

      <div class="success-steps">
        <div class="step" class:success-step={backedUp}>
          <div class="step-icon">{backedUp ? '✅' : '🔐'}</div>
          <div class="step-content">
            <h3>Back Up Your Keys</h3>
            <p>
              A unique pair of encryption keys was generated. They are like your password — if you
              lose them, you lose access to your account. Back them up before continuing.
            </p>
            {#if !backedUp}
              <button
                class="btn btn-primary"
                on:click={() => (showBackupModal = true)}
                disabled={backingUp}
              >
                {backingUp ? 'Backing up...' : 'Backup Keys'}
              </button>
            {:else}
              <p class="backed-up-confirmation">Keys backed up.</p>
            {/if}
          </div>
        </div>

        <div class="step" class:success-step={backedUp}>
          <div class="step-icon">{backedUp ? '✅' : '⏳'}</div>
          <div class="step-content">
            <h3>App Ready to Use!</h3>
            <p>Once your keys are backed up, you're all set to start posting and connecting with others!</p>
          </div>
        </div>
      </div>
    </div>
    <div class="action-buttons">
      <a href="/reeds" class="btn btn-primary" class:disabled={!backedUp} aria-disabled={!backedUp}>
        Start Posting!
      </a>
    </div>
  </div>

  <ExportDataModal
    open={showBackupModal}
    on:confirm={(e) => { showBackupModal = false; backupKeys(e.detail); }}
    on:cancel={() => (showBackupModal = false)}
  />
</Auth>

<style>
  .welcome-header {
    text-align: center;
    margin-bottom: 2rem;
  }

  .subtitle {
    color: var(--muted);
    font-size: 1.1rem;
    margin: 0;
  }

  .success-steps {
    margin: 2rem 0;
  }

  .step {
    display: flex;
    align-items: flex-start;
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .step-icon {
    font-size: 2rem;
    margin-right: 1rem;
    flex-shrink: 0;
  }

  .step-content h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .step-content p {
    margin: 0 0 0.75rem 0;
    color: var(--muted);
    line-height: 1.5;
  }

  .step-content p:last-child {
    margin-bottom: 0;
  }

  .backed-up-confirmation {
    color: #22c55e;
    font-weight: 600;
  }

  .success-step {
    background: rgba(34, 197, 94, 0.1);
    border: 1px solid rgba(34, 197, 94, 0.3);
  }

  .success-step .step-content h3 {
    color: #22c55e;
  }

  .action-buttons {
    display: flex;
    gap: 1rem;
    margin: 2rem 0;
    flex-wrap: wrap;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    transition: all 0.2s ease;
    border: none;
    cursor: pointer;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn:disabled,
  .btn.disabled {
    opacity: 0.6;
    cursor: not-allowed;
    pointer-events: none;
  }

  @media (max-width: 640px) {
    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }
  }
</style>
