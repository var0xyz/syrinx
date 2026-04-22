<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';

  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { cryptoService } from '$lib/services/crypto';
  import { getStorageQuota } from '$lib/services/pwa';
  import { dbService } from '$lib/services/db';
  import { localStorageService } from '$lib/services/localstorage';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import ExportDataModal from '$lib/components/ExportDataModal.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import UsernameChecker from '$lib/components/UsernameChecker.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import UserProfileCard from '$lib/components/UserProfileCard.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { formatRelativeTime } from '$lib/utils/time';
  import type { User } from '$lib/types/api';
  import { publicKeyRepository } from '$lib/repositories/publicKey';
  import { privateKeyRepository } from '$lib/repositories/privateKey';

  let user: User | null = null;
  let loading: boolean = true;
  let serverName: string = localStorage.getItem('serverName') || '';
  let storageUsed: number = 0;
  let storageTotal: number = 0;
  let storagePercentage: number = 0;
  let storageAvailable: boolean = false;

  // Edit mode state
  let isEditing: boolean = false;
  let editForm = {
    username: '',
    avatarURL: '',
    bio: ''
  };
  let editError: string = '';
  let editSuccess: string = '';
  let saving: boolean = false;

  // Encryption Key state
  let keyFingerprint: string | null = null;
  let keyIdentity: string = '';
  let publicKeyArmor: string = '';
  let loadingKeyInfo: boolean = false;
  let revoking: boolean = false;
  let showRevokeModal: boolean = false;
  let revokeReason: string = '';
  let revokeEmail: string = '';

  // Export state
  let exporting: boolean = false;
  let showExportWarningModal: boolean = false;
  let lastBackupAt: number | null = null;

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
      user = await authService.getCurrentUser();
      if (!user) {
        goto('/');
        return;
      }
    } catch (error) {
      console.error('Error checking authentication:', error);
      goto('/');
    }

    try {
      // Fetch storage quota information
      const storageData = await getStorageQuota();
      if (storageData) {
        storageUsed = storageData.used;
        storageTotal = storageData.total;
        storagePercentage = storageTotal > 0 ? (storageUsed / storageTotal) * 100 : 0;
        storageAvailable = true;
      }
    } catch (error) {
      console.error('Error fetching quota information:', error);
    }

    // Load encryption key information
    await loadKeyInfo();

    const storedBackupAt = localStorage.getItem('lastBackupAt');
    if (storedBackupAt) lastBackupAt = parseInt(storedBackupAt);

    loading = false;
  });

  async function loadKeyInfo(): Promise<void> {
    try {
      loadingKeyInfo = true;
      const fingerprint = authService.getActiveKeyFingerprint();
      if (!fingerprint) {
        return;
      }

      keyFingerprint = fingerprint;
      const publicKey = await publicKeyRepository.getPublicKey(fingerprint);
      if (publicKey) {
        publicKeyArmor = publicKey.armor;
        keyIdentity = await cryptoService.getKeyIdentity(publicKey.armor);
      }
      console.log("key identity:", keyIdentity);
    } catch (error) {
      console.error('Error loading key information:', error);
      notificationStore.error('Failed to load encryption key information');
    } finally {
      loadingKeyInfo = false;
    }
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
    if (!user || !keyFingerprint || !revokeReason.trim()) {
      notificationStore.error('Please provide a reason for revoking the key');
      return;
    }

    revoking = true;
    try {
      // Generate new key pair first (before revoking old key)
      const passphrase = authService.getPassphrase();
      if (!passphrase) {
        throw new Error('Passphrase not found');
      }

      const newKeyPair = await cryptoService.generateKeyPair({
        name: user.username || 'User',
        email: revokeEmail.trim() || undefined,
        password: passphrase
      });
      console.log("new key fingerprint:", newKeyPair.fingerprint);

      // Store new keys in IndexedDB
      await Promise.all([
        privateKeyRepository.put(newKeyPair.fingerprint, newKeyPair.privateKey),
        publicKeyRepository.put(newKeyPair.fingerprint, newKeyPair.publicKey)
      ]);

      // Get old key's private key from IndexedDB
      const oldPrivateKey = await privateKeyRepository.getPrivateKey(keyFingerprint);
      if (!oldPrivateKey) {
        throw new Error('Old private key not found');
      }

      // Sign the new public key with old private key
      const revokedKeySignature = await cryptoService.signMessage(
        newKeyPair.publicKey.trim(),
        oldPrivateKey.armor,
        passphrase
      );

      // Sign the new public key with new private key
      const newKeySignature = await cryptoService.signMessage(
        newKeyPair.publicKey.trim(),
        newKeyPair.privateKey,
        passphrase
      );

      // Revoke the old key
      await apiService.revokeKey(user.id, keyFingerprint, revokeReason.trim());

      // Set new key as active key
      authService.setActiveKey(newKeyPair.fingerprint);

      // Upload new public key with both signatures
      await apiService.addPublicKey(
        user.id,
        newKeyPair.publicKey,
        keyFingerprint,
        revokedKeySignature,
        newKeySignature
      );

      // Reload key info
      await loadKeyInfo();

      closeRevokeModal();
      notificationStore.success('Key revoked and new key generated successfully');
      window.location.reload();
    } catch (error) {
      console.error('Error revoking key:', error);
      notificationStore.error(
        error instanceof Error ? error.message : 'Failed to revoke key'
      );
    } finally {
      revoking = false;
    }
  }

  function startEditing(): void {
    if (!user) return;

    isEditing = true;
    editForm = {
      username: user.username || '',
      avatarURL: user.avatarURL || '',
      bio: user.bio || ''
    };
    editError = '';
    editSuccess = '';
  }

  function cancelEditing(): void {
    isEditing = false;
    editForm = {
      username: '',
      avatarURL: '',
      bio: ''
    };
    editError = '';
    editSuccess = '';
  }

  async function saveProfile(): Promise<void> {
    if (!user) return;

    saving = true;
    editError = '';
    editSuccess = '';

    try {
      // Validate bio length
      if (editForm.bio.length > 500) {
        editError = 'Bio cannot exceed 500 characters';
        return;
      }

      // Validate avatar URL if provided
      if (editForm.avatarURL && editForm.avatarURL.trim() !== '') {
        try {
          new URL(editForm.avatarURL);
        } catch {
          editError = 'Please enter a valid URL for the avatar';
          return;
        }
      }

      const updatedUser = await apiService.updateUser({
        username: editForm.username.trim() || undefined,
        avatarURL: editForm.avatarURL.trim() || undefined,
        bio: editForm.bio.trim() || undefined
      });

      // Update user data in localStorage, IndexedDB, and local state
      await authService.saveUserToStorage(updatedUser);
      user = updatedUser;
      editSuccess = 'Profile updated successfully!';

      // Clear success message after 3 seconds
      setTimeout(() => {
        editSuccess = '';
      }, 3000);

      // Exit edit mode
      isEditing = false;
    } catch (error) {
      console.error('Error updating profile:', error);
      editError = error instanceof Error ? error.message : 'Failed to update profile. Please try again.';
    } finally {
      saving = false;
    }
  }

  async function copyUserId(): Promise<void> {
    if (!user?.id) return;

    try {
      await navigator.clipboard.writeText(serverName ? `${user.id}@${serverName}` : user.id);
      notificationStore.success('User ID copied to clipboard');
    } catch (error) {
      console.error('Failed to copy user ID:', error);
      notificationStore.error('Failed to copy user ID');
    }
  }

  async function copyPublicKey(): Promise<void> {
    if (!publicKeyArmor) return;

    try {
      await navigator.clipboard.writeText(publicKeyArmor);
      notificationStore.success('Public key copied to clipboard');
    } catch (error) {
      console.error('Failed to copy public key:', error);
      notificationStore.error('Failed to copy public key');
    }
  }

  async function exportData(backupPassword: string): Promise<void> {
    if (!user) return;

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
          tables.push({
            name: tableName,
            items: items
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

      // Convert to JSON and compress using Compression Streams API
      const jsonString = JSON.stringify(exportData);

      // Create a ReadableStream from the JSON string
      const jsonBlob = new Blob([jsonString], { type: 'application/json' });
      const jsonStream = jsonBlob.stream();

      // Pipe through gzip compression
      const compressionStream = new CompressionStream('gzip');
      const compressedStream = jsonStream.pipeThrough(compressionStream);

      // Collect compressed chunks into a Uint8Array
      const chunks: Uint8Array[] = [];
      const reader = compressedStream.getReader();
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          if (value) {
            chunks.push(value);
          }
        }
      } finally {
        reader.releaseLock();
      }

      // Combine all chunks into a single Uint8Array
      const totalLength = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
      const compressedData = new Uint8Array(totalLength);
      let offset = 0;
      for (const chunk of chunks) {
        compressedData.set(chunk, offset);
        offset += chunk.length;
      }

      // Encrypt the compressed data with the user-provided password
      const encryptedData = await cryptoService.encryptBackup(compressedData, backupPassword);

      const filename = `${user.id}-${timestamp}.sxb.gz.gpg`;

      let exported = false;

      // Use File System Access API if available, otherwise fallback to download
      if ('showSaveFilePicker' in window) {
        try {
          const fileHandle = await (window as any).showSaveFilePicker({
            suggestedName: filename,
            types: [{
              description: 'Syrinx Encrypted Backup',
              accept: {
                'application/octet-stream': ['.sxb.gz.gpg']
              }
            }]
          });

          const writable = await fileHandle.createWritable();
          const blob = new Blob([encryptedData], { type: 'application/octet-stream' });
          await writable.write(blob);
          await writable.close();

          exported = true;
          notificationStore.success('Data exported successfully');
        } catch (error: any) {
          // User cancelled or error occurred
          if (error.name !== 'AbortError') {
            throw error;
          }
          // If user cancelled, don't show error
        }
      } else {
        // Fallback: create download link
        const blob = new Blob([encryptedData], { type: 'application/octet-stream' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);

        exported = true;
        notificationStore.success('Data exported successfully');
      }

      if (exported) {
        localStorage.setItem('lastBackupAt', timestamp.toString());
        lastBackupAt = timestamp;
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
</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading profile...</h2>
        <p>Please wait while we fetch your account information.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="profile-container">
    <!-- Main Content -->
    <div class="profile-content">
      {#if isEditing}
      <div class="profile-card">
        <div class="profile-info">
            <div class="edit-form">
              <div class="form-group">
                <label for="edit-username">Username</label>
                <input
                  id="edit-username"
                  type="text"
                  bind:value={editForm.username}
                  placeholder="Enter username"
                  maxlength="50"
                />
                {#if editForm.username && editForm.username !== user?.username}
                  <div class="help-text">
                    <UsernameChecker username={editForm.username} />
                  </div>
                {/if}
              </div>

              <div class="form-group">
                <label for="edit-avatar">Avatar URL</label>
                <input
                  id="edit-avatar"
                  type="url"
                  bind:value={editForm.avatarURL}
                  placeholder="https://example.com/avatar.jpg"
                />
              </div>

              <div class="form-group">
                <label for="edit-bio">Bio</label>
                <textarea
                  id="edit-bio"
                  bind:value={editForm.bio}
                  placeholder="Tell us about yourself..."
                  maxlength="500"
                  rows="3"
                ></textarea>
                <div class="char-count">{editForm.bio.length}/500</div>
              </div>

              {#if editError}
                <div class="error-message">
                  <p>{editError}</p>
                </div>
              {/if}

              {#if editSuccess}
                <div class="success-message">
                  <p>{editSuccess}</p>
                </div>
              {/if}

              <div class="edit-actions">
                <button
                  class="action-btn secondary"
                  on:click={cancelEditing}
                  disabled={saving}
                >
                  Cancel
                </button>
                <button
                  class="action-btn primary"
                  on:click={saveProfile}
                  disabled={saving}
                >
                  {saving ? 'Saving...' : 'Save Changes'}
                </button>
              </div>
            </div>
        </div>
      </div>
      {:else}
        <UserProfileCard {user} editable={true} on:edit={startEditing} />
      {/if}

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
          {:else if keyFingerprint}
            <div class="encryption-key-info">
              <div class="key-field">
                <strong>Fingerprint</strong>
                <div class="key-value fingerprint">{keyFingerprint}</div>
              </div>
              <div class="key-field">
                <strong>Identity</strong>
                <div class="key-value">{keyIdentity || 'Unknown'}</div>
              </div>
              <div class="key-field">
                <div class="key-armor-container">
                  <div class="key-armor-label">
                    <strong>Public Key</strong>
                  </div>
                  {#if publicKeyArmor}
                    <CopyButton ariaLabel="Copy public key" on:click={copyPublicKey} />
                  {/if}
                </div>
                <div class="key-value public-key">{publicKeyArmor || 'Not available'}</div>
              </div>
              <div class="key-actions">
                <button
                  class="action-btn danger"
                  on:click={openRevokeModal}
                  disabled={revoking}
                >
                  Revoke Key
                </button>
              </div>
            </div>
          {:else}
            <div class="key-unavailable">
              <p>No encryption key found</p>
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
            on:click={closeRevokeModal}
            on:keydown={(e) => e.key === 'Escape' && closeRevokeModal()}
          >
            <div
              class="modal-content"
              role="document"
              on:click={(e) => e.stopPropagation()}
            >
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
              <button class="action-btn primary" on:click={() => showExportWarningModal = true} disabled={exporting}>
                {exporting ? 'Exporting...' : 'Export Data'}
              </button>
              {#if lastBackupAt}
                <span class="last-backup">Last backup {formatRelativeTime(lastBackupAt)}</span>
              {/if}
            </div>
            <button class="action-btn danger" on:click={() => goto('/delete/confirm')}>Delete Account</button>
          </div>
        </div>
      </div>
    </div>

    <ExportDataModal
      open={showExportWarningModal}
      on:confirm={(e) => { showExportWarningModal = false; exportData(e.detail); }}
      on:cancel={() => showExportWarningModal = false}
    />

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="profile" />
    </div>
  </Auth>
{/if}

<style>
  .profile-container {
    min-height: calc(100vh - 4rem - 1px);
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

  .profile-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
    text-align: center;
    margin-bottom: 1rem;
  }

  .action-btn {
    padding: 0.5rem 1rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.9rem;
    border: 1px solid var(--border);
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



  /* Edit Form Styles */
  .edit-form {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    margin: 1rem 0;
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

  .char-count {
    color: var(--muted);
    font-size: 0.8rem;
    text-align: right;
  }

  .edit-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 1rem;
    justify-content: center;
  }

  .success-message {
    background: rgba(76, 175, 80, 0.1);
    border: 1px solid rgba(76, 175, 80, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    margin: 1rem 0;
  }

  .success-message p {
    margin: 0;
    color: #4caf50;
    font-size: 0.9rem;
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


  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
  }

  /* Encryption Key Styles */
  .key-loading,
  .key-unavailable {
    text-align: center;
    padding: 1rem;
    color: var(--muted);
  }

  .key-loading p,
  .key-unavailable p {
    margin: 0;
  }

  .encryption-key-info {
    display: flex;
    flex-direction: column;
    gap: 1rem;
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

  .key-armor-container {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .key-armor-label {
    flex-shrink: 0;
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
    overflow: scroll;
    white-space: pre;
  }

  .key-value.fingerprint {
    font-size: 0.85rem;
    letter-spacing: 0.5px;
  }

  .key-value.public-key {
    max-height: 200px;
    font-size: 0.75rem;
    line-height: 1.4;
  }

  .key-actions {
    margin-top: 0.5rem;
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
    .profile-card {
      padding: 0 0.75rem;
      margin-bottom: 0.5rem;
    }

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
