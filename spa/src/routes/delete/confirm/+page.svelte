<script lang="ts">
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { dbService } from '$lib/services/db';
  import { localStorageService } from '$lib/services/localstorage';
  import { notificationStore } from '$lib/stores/notifications';

  let deleting = false;
  let error: string = '';

  async function handleDeleteAccount(): Promise<void> {
    if (deleting) return;

    deleting = true;
    error = '';

    try {
      // Call the API to delete the account
      await apiService.deleteAccount();

      // Clear localStorage
      localStorageService.clearAllData();

      // Delete IndexedDB database
      await dbService.deleteDatabase();

      // Clear service worker session and disconnect WebSocket
      await authService.clearSession();

      // Redirect to goodbye page
      goto('/goodbye');
    } catch (err) {
      console.error('Error deleting account:', err);
      error = err instanceof Error ? err.message : 'Failed to delete account. Please try again.';
      notificationStore.error(error);
      deleting = false;
    }
  }

  function handleCancel(): void {
    goto('/profile');
  }
</script>

<div class="container">
  <div class="card">
    <h1>Delete Account</h1>
    <p class="subtitle">This action cannot be undone</p>

    <div class="content">
      <div class="warning-section">
        <h2>⚠️ Important Information</h2>
        <p class="intro">Before proceeding, please understand the following:</p>

        <div class="warning-item info-item">
          <h3>📱 All data will be deleted from your device</h3>
          <p>This includes your private keys, reeds, and all local storage.</p>
        </div>

        <div class="warning-item info-item">
          <h3>👤 Your profile will be deleted from the server</h3>
          <p>Your account and profile information will be permanently removed.</p>
        </div>

        <div class="warning-item">
          <h3>📤 Reeds distributed to other users</h3>
          <p>If you posted any reeds, users who received them will be asked to delete them, but deletion cannot be guaranteed.</p>
        </div>

        <div class="warning-item">
          <h3>🔑 Your public keys will not be deleted</h3>
          <p>Public keys are retained to prevent impersonation and maintain cryptographic integrity.</p>
        </div>
      </div>
    </div>

    {#if error}
      <div class="error-message">
        <p>{error}</p>
      </div>
    {/if}

    <div class="action-buttons">
      <button
        class="btn btn-secondary"
        on:click={handleCancel}
        disabled={deleting}
      >
        Cancel
      </button>
      <button
        class="btn btn-danger"
        on:click={handleDeleteAccount}
        disabled={deleting}
      >
        {deleting ? 'Deleting...' : 'I understand, delete my account'}
      </button>
    </div>
  </div>
</div>

<style>
  .container {
    max-width: 800px;
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
    text-align: left;
  }

  .card h1 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 2rem;
    text-align: center;
  }

  .subtitle {
    margin: 0 0 1.5rem 0;
    color: var(--muted);
    font-size: 1.1rem;
    text-align: center;
  }

  .content {
    margin-bottom: 2rem;
  }

  .intro {
    font-size: 1.1rem;
    line-height: 1.6;
    margin-bottom: 1.5rem;
    text-align: center;
  }

  .warning-section {
    margin-bottom: 2rem;
  }

  .warning-section h2 {
    color: #f59e0b;
    font-size: 1.3rem;
    margin-bottom: 1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item {
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: rgba(245, 158, 11, 0.1);
    border-left: 4px solid #f59e0b;
    border-radius: 8px;
  }

  .warning-item.info-item {
    background: transparent;
    border-left: 4px solid #10b981;
  }

  .warning-item h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item p {
    margin: 0;
    line-height: 1.5;
  }

  .error-message {
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: rgba(244, 67, 54, 0.1);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-left: 4px solid var(--error);
    border-radius: 8px;
  }

  .error-message p {
    margin: 0;
    color: var(--error);
    font-size: 0.9rem;
  }

  .action-buttons {
    border-top: 1px solid var(--border);
    padding-top: 2rem;
    text-align: center;
    display: flex;
    gap: 1rem;
    justify-content: center;
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
    font-size: 0.95rem;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn:not(:disabled):hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover:not(:disabled) {
    background: var(--input-bg);
    border-color: var(--primary);
  }

  .btn-danger {
    background: var(--error);
    color: white;
  }

  @media (max-width: 640px) {
    .container {
      padding: 0.5rem;
    }

    .card {
      padding: 1.5rem;
    }

    .card h1 {
      font-size: 1.5rem;
    }

    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }

    .warning-item {
      padding: 0.75rem;
    }

    .warning-item h3 {
      font-size: 1rem;
    }
  }
</style>

