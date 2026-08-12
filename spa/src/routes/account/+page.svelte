<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';

  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { cryptoService } from '$lib/services/crypto';
  import { buildUserRevocationPayload } from '$lib/services/signing';
  import { requestSigner } from '$lib/services/request-signer';
  import { isOnline } from '$lib/services/pwa';
  import { dbService } from '$lib/services/db';
  import { localStorageService } from '$lib/services/localstorage';
  import { compressBackupPayload, encryptAndSaveBackup, buildKeyBackupPayload } from '$lib/services/backupRestore';
  import { recordBackupEvent } from '$lib/services/backupMetrics';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ExportDataModal from '$lib/components/ExportDataModal.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { formatRelativeTime } from '$lib/utils/time';
  import { publicKeyRepository } from '$lib/repositories/publicKey';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { pendingRevocationRepository, pendingRevocationSynced } from '$lib/repositories/pendingRevocation';
  import { revocationRepository } from '$lib/repositories/revocation';
  import { loadProfileKeyInfo, type ProfileKeyInfo } from './keyInfo';
  import { mergeUserView, type UserView } from '$lib/utils/userView';

  /** @type {import('./$types').PageData} */
  export let data;

  let user: UserView = data.user;
  let storageUsed: number = data.storage?.used ?? 0;
  let storageTotal: number = data.storage?.total ?? 0;
  let storagePercentage: number = storageTotal > 0 ? (storageUsed / storageTotal) * 100 : 0;
  let storageAvailable: boolean = data.storage != null;

  // Encryption Key state (seeded from page load)
  let keyFingerprint: string = data.keyInfo.fingerprint;
  let keyIdentity: string = data.keyInfo.identity;
  let loadingKeyInfo: boolean = false;
  let revoking: boolean = false;
  let showRevokeModal: boolean = false;
  let revokeReason: string = '';
  let revokeEmail: string = '';
  let isPendingRevocation: boolean = data.keyInfo.isPendingRevocation;
  let isKeyRevoked: boolean = data.keyInfo.isKeyRevoked;
  let revokedInfo: ProfileKeyInfo['revokedInfo'] = data.keyInfo.revokedInfo;

  // Export state
  let exporting: boolean = false;
  let showExportWarningModal: boolean = false;
  let lastBackupAt: number | null = null;

  // Key backup state
  let backingUpKeys: boolean = false;
  let showBackupKeysModal: boolean = false;
  let lastKeyBackupAt: number | null = null;
  let activeKeyMintedAt: number | null = null;
  // Never backed up, or backed up before the currently active key was minted
  // (e.g. after a revoke) — the existing backup no longer covers this key.
  $: keyBackupStale = !lastKeyBackupAt || (activeKeyMintedAt != null && lastKeyBackupAt < activeKeyMintedAt);

  // Helper function to format bytes into human-readable format
  function formatBytes(bytes: number): string {
    console.log(`bytes: ${bytes}`);
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  // Helper function to format date into user-friendly format
  function formatDate(dateString: string): string {
    try {
      const date = new Date(dateString);
      return date.toLocaleDateString('en-US', {
        year: 'numeric',
        month: 'long',
        day: 'numeric'
      });
    } catch (error) {
      return dateString; // Return original string if parsing fails
    }
  }


  onMount(async () => {
    try {
      const [freshProfile, freshInfo] = await Promise.all([
        apiService.getUserProfile(user.id),
        apiService.getUserInfo(user.id),
      ]);
      await authService.saveUserToStorage(freshProfile);
      await userInfoRepository.put(freshInfo);
      user = mergeUserView(freshProfile, freshInfo)!;
    } catch (error) {
      console.error('Failed to refresh profile:', error);
    }

    const storedBackupAt = localStorage.getItem('lastBackupAt');
    if (storedBackupAt) lastBackupAt = parseInt(storedBackupAt);

    const storedKeyBackupAt = localStorage.getItem('lastKeyBackupAt');
    if (storedKeyBackupAt) lastKeyBackupAt = parseInt(storedKeyBackupAt);

    await refreshActiveKeyMintedAt();
  });

  async function refreshActiveKeyMintedAt(): Promise<void> {
    activeKeyMintedAt = await privateKeyRepository.getMintedAt(keyFingerprint);
  }

  function applyKeyInfo(info: ProfileKeyInfo): void {
    keyFingerprint = info.fingerprint;
    keyIdentity = info.identity;
    isPendingRevocation = info.isPendingRevocation;
    isKeyRevoked = info.isKeyRevoked;
    revokedInfo = info.revokedInfo;
    void refreshActiveKeyMintedAt();
  }

  async function loadKeyInfo(): Promise<void> {
    try {
      loadingKeyInfo = true;
      applyKeyInfo(await loadProfileKeyInfo());
    } catch (error) {
      console.error('Error loading key information:', error);
      notificationStore.error('Failed to load encryption key information');
    } finally {
      loadingKeyInfo = false;
    }
  }

  // Re-load key info when a background sync completes (skip initial store read)
  let seenPendingSync = $pendingRevocationSynced;
  $: if ($pendingRevocationSynced !== seenPendingSync) {
    seenPendingSync = $pendingRevocationSynced;
    void loadKeyInfo();
  }

  function openRevokeModal(): void {
    showRevokeModal = true;
    revokeReason = '';
    revokeEmail = '';
  }

  function closeRevokeModal(): void {
    showRevokeModal = false;
    revokeReason = '';
    revokeEmail = '';
  }

  async function revokeKey(): Promise<void> {
    if (!revokeReason.trim()) {
      notificationStore.error('Please provide a reason for revoking the key');
      return;
    }

    revoking = true;
    const oldFingerprint = keyFingerprint;
    const reason = revokeReason.trim();
    try {
      // Generate new key pair first (before revoking old key)
      const passphrase = authService.getPassphrase();
      if (!passphrase) {
        throw new Error('Passphrase not found');
      }

      const serverId = localStorage.getItem('serverId') || '';
      const serverName = localStorage.getItem('serverName') || '';
      const newKeyPair = await cryptoService.generateKeyPair({
        name: `${user.id}@${serverId}`,
        email: revokeEmail.trim() || undefined,
        comment: serverName || undefined,
        password: passphrase
      });
      console.log("new key fingerprint:", newKeyPair.fingerprint);

      // Store the new private key locally. The public key is cached only
      // after AddPublicKey returns the server countersignature.
      await privateKeyRepository.put(newKeyPair.fingerprint, newKeyPair.privateKey);

      // Get old key's private key from IndexedDB
      const oldPrivateKey = await privateKeyRepository.getPrivateKey(oldFingerprint);
      if (!oldPrivateKey) {
        throw new Error('Old private key not found');
      }

      // Sign the user revocation attestation with the old private key.
      const userRevocationPayload = buildUserRevocationPayload(user.id, oldFingerprint, reason);
      const userRevocationSigArmor = await cryptoService.signMessage(
        userRevocationPayload,
        oldPrivateKey.armor,
        passphrase
      );
      const userRevocationSignature = btoa(userRevocationSigArmor);

      // Sign the new public key with old private key (rotation proof).
      const revokedKeySignature = btoa(await cryptoService.signMessage(
        newKeyPair.publicKey.trim(),
        oldPrivateKey.armor,
        passphrase
      ));

      // Sign the new public key with new private key
      const newKeySignature = btoa(await cryptoService.signMessage(
        newKeyPair.publicKey.trim(),
        newKeyPair.privateKey,
        passphrase
      ));

      // Store pending revocation so it can be retried if the server call fails
      await pendingRevocationRepository.put({
        fingerprint: oldFingerprint,
        reason,
        userId: user.id,
        newFingerprint: newKeyPair.fingerprint,
        newPublicKey: newKeyPair.publicKey,
        userRevocationSignature,
        revokedKeySignature,
        newKeySignature,
      });

      closeRevokeModal();
      isPendingRevocation = true;
      isKeyRevoked = true;
      const progressNotificationId = notificationStore.info('Key revocation in progress...');

      try {
        // Revoke the old key — server returns the key with revoked: true.
        const revokedKey = await apiService.revokeKey(
          user.id,
          oldFingerprint,
          reason,
          userRevocationSignature
        );
        await publicKeyRepository.setRevoked(revokedKey);
        await privateKeyRepository.setRevoked(oldFingerprint);

        // Mark revokeKey as done so a retry skips it and goes straight to addPublicKey
        await pendingRevocationRepository.markRevoked(oldFingerprint);

        // Upload new public key; response is the full wire PublicKey.
        const newPublicKey = await apiService.addPublicKey(
          user.id,
          btoa(newKeyPair.publicKey),
          oldFingerprint,
          revokedKeySignature,
          newKeySignature
        );
        await publicKeyRepository.put(newPublicKey);

        // Switch to the new key, then fetch the countersigned revocation proof.
        authService.setActiveKey(newKeyPair.fingerprint);
        await requestSigner.initializeWorker(newKeyPair.fingerprint, passphrase);

        const revocation = await apiService.getKeyRevocation(user.id, oldFingerprint);
        await revocationRepository.put(revocation);

        await pendingRevocationRepository.delete(oldFingerprint);
        notificationStore.dismiss(progressNotificationId);
        isPendingRevocation = false;
        await loadKeyInfo();
        notificationStore.success('Key revoked and new key generated successfully');
      } catch (serverError) {
        // Leave pending record in place; syncPending() will retry on reconnect
        console.error('Server revocation failed, will retry on reconnect:', serverError);
        notificationStore.dismiss(progressNotificationId);
        notificationStore.info('Revocation queued — will complete when back online');
      }
    } catch (error) {
      console.error('Error revoking key:', error);
      notificationStore.error(
        error instanceof Error ? error.message : 'Failed to revoke key'
      );
    } finally {
      revoking = false;
    }
  }

  async function exportData(backupPassword: string): Promise<void> {
    exporting = true;
    try {
      const timestamp = Date.now();

      // Collect all localStorage items
      const localStorageData = localStorageService.getAll();

      // Collect all IndexedDB tables dynamically
      const tableNames = await dbService.getTableNames();
      const tables: Array<{ name: string; items: unknown[] }> = [];

      for (const tableName of tableNames) {
        try {
          const items = await dbService.getAll(tableName);
          const isKeyTable = tableName === 'publicKeys' || tableName === 'privateKeys';
          tables.push({
            name: tableName,
            items: isKeyTable
              ? items.map((item) => ({ ...(item as { armor: string }), armor: btoa((item as { armor: string }).armor) }))
              : items
          });
        } catch (error) {
          console.error(`Error reading table ${tableName}:`, error);
          // Continue with other tables even if one fails
          tables.push({
            name: tableName,
            items: []
          });
        }
      }

      // Build the export data structure
      const exportData = {
        timestamp,
        origin: window.location.origin,
        localStorage: localStorageData,
        indexedDB: {
          name: 'Syrinx',
          tables: tables
        }
      };

      const compressedData = await compressBackupPayload(exportData);
      const filename = `syrinx-${user.id}-${timestamp}.sxb.gpg`;
      const exported = await encryptAndSaveBackup(compressedData, backupPassword, filename);

      if (exported) {
        notificationStore.success('Data exported successfully');
        localStorage.setItem('lastBackupAt', timestamp.toString());
        lastBackupAt = timestamp;
        void recordBackupEvent('full');
      }
    } catch (error) {
      console.error('Error exporting data:', error);
      notificationStore.error(
        error instanceof Error ? error.message : 'Failed to export data'
      );
    } finally {
      exporting = false;
    }
  }

  async function backupKeys(password: string): Promise<void> {
    backingUpKeys = true;
    try {
      const payload = await buildKeyBackupPayload();
      const compressedData = await compressBackupPayload(payload);
      const filename = `syrinx-${user.id}-${payload.timestamp}.sxi.gpg`;
      const saved = await encryptAndSaveBackup(compressedData, password, filename, 'identity');

      if (saved) {
        notificationStore.success('Keys backed up successfully');
        localStorage.setItem('lastKeyBackupAt', String(payload.timestamp));
        lastKeyBackupAt = payload.timestamp;
        void recordBackupEvent('identity');
      }
    } catch (error) {
      console.error('Error backing up keys:', error);
      notificationStore.error(
        error instanceof Error ? error.message : 'Failed to back up keys'
      );
    } finally {
      backingUpKeys = false;
    }
  }
</script>

<Auth>
  <div class="profile-container">
    <!-- Main Content -->
    <div class="profile-content">
      <div class="profile-sections">
        <!-- Storage Usage -->
        <div class="section">
          <h3>💾 Storage Usage</h3>
          {#if storageAvailable}
            <div class="storage-info">
              <div class="storage-stats">
                <div class="storage-item">
                  <span class="label">Used</span>
                  <span class="value">{formatBytes(storageUsed)}</span>
                </div>
                <div class="storage-item">
                  <span class="label">Total</span>
                  <span class="value">{formatBytes(storageTotal)}</span>
                </div>
                <div class="storage-item">
                  <span class="label">Percentage</span>
                  <span class="value storage-percentage" class:low-usage={storagePercentage < 50} class:medium-usage={storagePercentage >= 50 && storagePercentage < 80} class:high-usage={storagePercentage >= 80}>
                    {storagePercentage.toFixed(1)}%
                  </span>
                </div>
              </div>
              <div class="storage-progress">
                <div class="progress-bar">
                  <div
                    class="progress-fill"
                    class:low-usage={storagePercentage < 50}
                    class:medium-usage={storagePercentage >= 50 && storagePercentage < 80}
                    class:high-usage={storagePercentage >= 80}
                    style="width: {storagePercentage}%"
                  ></div>
                </div>
                <div class="progress-labels">
                  <span>0%</span>
                  <span>100%</span>
                </div>
              </div>
            </div>
          {:else}
            <div class="storage-unavailable">
              <p>Storage information is not available in this browser.</p>
            </div>
          {/if}
        </div>

        <!-- Encryption Key -->
        <div class="section">
          <h3>🔑 Encryption Key</h3>
          {#if loadingKeyInfo}
            <div class="key-loading">
              <p>Loading key information...</p>
            </div>
          {:else}
            <div class="encryption-key-info" class:key-revoked={isKeyRevoked}>
              {#if isKeyRevoked}
                <div class="revoked-stamp">REVOKED</div>
                {#if revokedInfo}
                  <div class="key-field">
                    <strong>Revoked</strong>
                    <div class="key-value">{new Date(revokedInfo.timestamp).toLocaleString()} — {revokedInfo.reason}</div>
                  </div>
                {/if}
              {/if}
              <div class="key-field">
                <strong>Fingerprint</strong>
                <div class="key-value fingerprint">{keyFingerprint}</div>
              </div>
              <div class="key-field">
                <strong>Identity</strong>
                <div class="key-value">{keyIdentity || 'Unknown'}</div>
              </div>
              <div class="key-backup">
                {#if keyBackupStale}
                  <span class="key-backup-warning">
                    ⚠️ {lastKeyBackupAt
                      ? 'Your key backup is outdated — back up again to protect your current key.'
                      : 'Your keys have never been backed up.'}
                  </span>
                {:else}
                  <span class="last-backup">Last key backup {formatRelativeTime(lastKeyBackupAt)}</span>
                {/if}
                <button
                  class="action-btn primary"
                  on:click={() => showBackupKeysModal = true}
                  disabled={backingUpKeys || isKeyRevoked || isPendingRevocation}
                >
                  {backingUpKeys ? 'Backing up...' : 'Backup Keys'}
                </button>
              </div>
              <div class="key-actions">
                {#if isPendingRevocation}
                  {#if !$isOnline}
                    <div class="key-pending-banner">
                      ⚠️ Key revocation is pending — will sync when back online.
                    </div>
                  {/if}
                  <button class="action-btn" disabled>Revoking...</button>
                {:else if isKeyRevoked}
                  <button class="action-btn primary" on:click={loadKeyInfo}>
                    Show new key
                  </button>
                {:else}
                  <button
                    class="action-btn danger"
                    on:click={openRevokeModal}
                    disabled={revoking}
                  >
                    Revoke Key
                  </button>
                {/if}
              </div>
            </div>
          {/if}
        </div>

        <!-- Revoke Key Modal -->
        {#if showRevokeModal}
          <div
            class="modal-overlay"
            role="dialog"
            aria-modal="true"
            aria-labelledby="revoke-modal-title"
            tabindex="-1"
            on:click={(e) => e.target === e.currentTarget && closeRevokeModal()}
            on:keydown={(e) => e.key === 'Escape' && closeRevokeModal()}
          >
            <div class="modal-content">
              <h3 id="revoke-modal-title">Revoke Encryption Key</h3>
              <p class="modal-warning">
                Note: A new pair will be automatically generated and the public key uploaded to the server.
              </p>
              <div class="form-group">
                <label for="revoke-reason">Reason</label>
                <textarea
                  id="revoke-reason"
                  bind:value={revokeReason}
                  placeholder="e.g., Private key was compromised"
                  rows="3"
                  required
                ></textarea>
              </div>
              <div class="form-group">
                <label for="revoke-email">Email</label>
                <input
                  id="revoke-email"
                  type="email"
                  bind:value={revokeEmail}
                  placeholder="Optional"
                  autocomplete="email"
                />
                <div class="help-text">
                  Used for your new encryption key identity, won't be verified
                </div>
              </div>
              <div class="modal-actions">
                <button
                  class="action-btn secondary"
                  on:click={closeRevokeModal}
                  disabled={revoking}
                >
                  Cancel
                </button>
                <button
                  class="action-btn danger"
                  on:click={revokeKey}
                  disabled={revoking || !revokeReason.trim()}
                >
                  {revoking ? 'Revoking...' : 'Revoke Key'}
                </button>
              </div>
            </div>
          </div>
        {/if}

        <!-- Actions -->
        <div class="section">
          <h3>🚪 Account Actions</h3>
          <div class="action-buttons">
            <div class="export-group">
              {#if lastBackupAt}
                <span class="last-backup">Last full backup {formatRelativeTime(lastBackupAt)}</span>
              {/if}
              <button class="action-btn primary" on:click={() => showExportWarningModal = true} disabled={exporting}>
                {exporting ? 'Exporting...' : 'Export Data'}
              </button>
            </div>
            <button class="action-btn danger" on:click={() => goto('/delete/confirm')}>Delete Account</button>
          </div>
        </div>
      </div>
    </div>

    <p class="app-version">Version: {__APP_VERSION__.slice(0, 12)}</p>

    <ExportDataModal
      open={showExportWarningModal}
      on:confirm={(e) => { showExportWarningModal = false; exportData(e.detail); }}
      on:cancel={() => showExportWarningModal = false}
    />

    <ExportDataModal
      open={showBackupKeysModal}
      on:confirm={(e) => { showBackupKeysModal = false; backupKeys(e.detail); }}
      on:cancel={() => showBackupKeysModal = false}
    />

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="account" />
  </div>
</Auth>

<style>
  .profile-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .profile-content {
    flex: 1;
    max-width: 800px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .app-version {
    text-align: center;
    color: var(--muted);
    font-size: 0.75rem;
    font-family: monospace;
    margin: 0 0 1rem;
  }


  .action-btn {
    padding: 0.5rem 1rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.9rem;
    border: 1px solid var(--border);
  }

  .action-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .action-btn.secondary {
    background: var(--surface);
    color: var(--fg);
    border-color: var(--border);
  }

  .action-btn.secondary:hover {
    background: var(--input-bg);
    border-color: var(--primary);
  }

  .profile-sections {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .section {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
  }

  .section h3 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }


  .label {
    color: var(--muted);
    font-size: 0.8rem;
    font-weight: 500;
  }

  .value {
    color: var(--fg);
    font-size: 0.9rem;
  }



  .form-group {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .form-group label {
    color: var(--fg);
    font-weight: 600;
    font-size: 0.9rem;
  }

  .form-group input,
  .form-group textarea {
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
  }

  .form-group input:focus,
  .form-group textarea:focus {
    outline: none;
    border-color: var(--primary);
  }

  .form-group textarea {
    resize: vertical;
    min-height: 80px;
  }

  /* Storage Usage Styles */
  .storage-info {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .storage-stats {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 1rem;
  }

  .storage-item {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    text-align: center;
  }

  .storage-percentage {
    font-weight: 600;
  }

  .storage-percentage.low-usage {
    color: #4caf50;
  }

  .storage-percentage.medium-usage {
    color: #ff9800;
  }

  .storage-percentage.high-usage {
    color: #f44336;
  }

  .storage-progress {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .progress-bar {
    width: 100%;
    height: 12px;
    background-color: var(--input-bg);
    border-radius: 6px;
    overflow: hidden;
    position: relative;
  }

  .progress-fill {
    height: 100%;
    transition: width 0.3s ease;
    border-radius: 6px;
  }

  .progress-fill.low-usage {
    background: linear-gradient(90deg, #4caf50, #66bb6a);
  }

  .progress-fill.medium-usage {
    background: linear-gradient(90deg, #ff9800, #ffb74d);
  }

  .progress-fill.high-usage {
    background: linear-gradient(90deg, #f44336, #ef5350);
  }

  .progress-labels {
    display: flex;
    justify-content: space-between;
    font-size: 0.7rem;
    color: var(--muted);
  }

  .storage-unavailable {
    text-align: center;
    padding: 1rem;
    color: var(--muted);
  }

  .storage-unavailable p {
    margin: 0;
    font-style: italic;
  }

  .action-buttons {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .export-group {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .last-backup {
    font-size: 0.8rem;
    color: var(--muted);
  }

  .action-btn {
    padding: 0.75rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    font-weight: 600;
  }

  .action-btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }


  .action-btn.danger {
    background: var(--error);
    color: white;
  }

  .action-btn:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  /* Encryption Key Styles */
  .key-loading {
    text-align: center;
    padding: 1rem;
    color: var(--muted);
  }

  .key-loading p {
    margin: 0;
  }

  .encryption-key-info {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .key-field {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .key-field strong {
    color: var(--muted);
    font-size: 0.85rem;
    font-weight: 600;
  }

  .key-value {
    color: var(--fg);
    font-size: 0.9rem;
    padding: 0.75rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    word-break: break-all;
    font-family: monospace;
    overflow: auto;
    white-space: pre;
  }

  .key-value.fingerprint {
    font-size: 0.85rem;
    letter-spacing: 0.5px;
  }

  .key-backup {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.4rem;
    margin-top: 0.5rem;
  }

  .key-backup-warning {
    font-size: 0.85rem;
    color: var(--error);
    font-weight: 600;
  }

  .key-pending-banner {
    padding: 0.75rem 1rem;
    background: #fffbe6;
    border: 1px solid #ffe58f;
    border-radius: 6px;
    color: #7c5c00;
    font-size: 0.9rem;
    margin-bottom: 0.5rem;
  }

  .encryption-key-info.key-revoked {
    position: relative;
  }

  .encryption-key-info.key-revoked > * :not(.revoked-stamp):not(.action-btn) {
    opacity: 0.55;
    filter: grayscale(0.6);
  }

  .revoked-stamp {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%) rotate(-15deg);
    font-size: 2.5rem;
    font-weight: 900;
    color: #cc0000;
    border: 4px solid #cc0000;
    padding: 0.25rem 0.75rem;
    border-radius: 4px;
    pointer-events: none;
    z-index: 1;
    letter-spacing: 4px;
  }

  /* Modal Styles */
  .modal-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    padding: 1rem;
  }

  .modal-content {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    max-width: 500px;
    width: 100%;
    max-height: 90vh;
    overflow-y: auto;
  }

  .modal-content .form-group {
    margin: 1rem 0;
  }

  .modal-content .help-text {
    font-size: 0.8rem;
    color: var(--muted);
  }

  .modal-content h3 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 1.2rem;
  }

  .modal-warning {
    color: var(--fg);
    font-size: 0.9rem;
    margin: 0 0 1rem 0;
    padding: 0.75rem;
    background: rgba(54, 244, 133, 0.1);
    border: 1px solid rgba(54, 244, 133, 0.3);
    border-radius: 6px;
  }

  .modal-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 1.5rem;
    justify-content: flex-end;
  }

  /* Responsive Design */
  @media (max-width: 768px) {
    .profile-content {
      padding: 0.5rem;
    }

    .profile-sections {
      gap: 0.5rem;
    }

    .section {
      padding: 0.75rem;
    }

    .storage-stats {
      grid-template-columns: 1fr;
      gap: 0.75rem;
    }

    .storage-item {
      text-align: left;
    }

    .action-btn {
      width: 100%;
      border-color: var(--border);
    }

  }
</style>
