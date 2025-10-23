<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';

  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { getStorageQuota, isBiometricSupported, isBiometricEnabled, disableBiometric, createBiometricCredential } from '$lib/services/pwa';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import type { User } from '$lib/types/api';

  let user: User | null = null;
  let loading: boolean = true;
  let storageUsed: number = 0;
  let storageTotal: number = 0;
  let storagePercentage: number = 0;
  let storageAvailable: boolean = false;
  let biometricSupported: boolean = false;
  let biometricEnabled: boolean = false;
  let biometricSetupInProgress: boolean = false;
  let biometricSetupError: string = '';

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

  // Helper function to format bytes into human-readable format
  function formatBytes(bytes: number): string {
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

      // Fetch storage quota information
      const storageData = await getStorageQuota();
      if (storageData) {
        storageUsed = storageData.used;
        storageTotal = storageData.total;
        storagePercentage = storageTotal > 0 ? (storageUsed / storageTotal) * 100 : 0;
        storageAvailable = true;
      }

      // Check biometric support and status
      biometricSupported = await isBiometricSupported();
      biometricEnabled = await isBiometricEnabled();
    } catch (error) {
      console.error('Error checking authentication:', error);
      goto('/');
    } finally {
      loading = false;
    }
  });

  async function handleEnableBiometric(): Promise<void> {
    if (!biometricSupported || !user) return;

    biometricSetupInProgress = true;
    biometricSetupError = '';

    try {
      console.log('PWA: Setting up biometric with user:', user);
      console.log('PWA: user.id:', user.id, 'user.username:', user.username);
      const success = await createBiometricCredential(user.id, user.username);
      if (success) {
        biometricEnabled = true;
      } else {
        biometricSetupError = 'Failed to set up biometric authentication. Please try again.';
      }
    } catch (error) {
      console.error('Error setting up biometric:', error);
      biometricSetupError = 'An error occurred while setting up biometric authentication.';
    } finally {
      biometricSetupInProgress = false;
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
      <div class="profile-card">
        <div class="profile-info">
          {#if isEditing}
            <div class="edit-form">
              <div class="profile-avatar">
                <img
                  src={user?.avatarURL || '/static/default-avatar.png'}
                  alt="{user?.username}'s Avatar"
                  class="avatar-large"
                  on:error={(e) => (e.target as HTMLImageElement).src = '/static/default-avatar.png'}
                />
                <button class="edit-avatar-btn">Edit</button>
              </div>
              <div class="form-group">
                <label for="edit-username">Username</label>
                <input
                  id="edit-username"
                  type="text"
                  bind:value={editForm.username}
                  placeholder="Enter username"
                  maxlength="50"
                />
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
                  class="action-btn primary"
                  on:click={saveProfile}
                  disabled={saving}
                >
                  {saving ? 'Saving...' : 'Save Changes'}
                </button>
                <button
                  class="action-btn secondary"
                  on:click={cancelEditing}
                  disabled={saving}
                >
                  Cancel
                </button>
              </div>
            </div>
          {:else}
                <p class="user-id">{user?.id}</p>
            <div class="profile-header">
              <div class="avatar-container">
                <img
                  src={user?.avatarURL || '/static/default-avatar.png'}
                  alt="{user?.username}'s Avatar"
                  class="profile-avatar"
                  on:error={(e) => (e.target as HTMLImageElement).src = '/static/default-avatar.png'}
                />
              </div>
              <div class="profile-info">
                <h2>{user?.username}</h2>
                {#if user?.bio}
                  <p class="user-bio">{user.bio}</p>
                {/if}
              </div>
            </div>
            <div class="profile-details">
              <div class="detail-item">
                <span class="label">Member Since</span>
                <span class="value">{user?.memberSince ? formatDate(user.memberSince) : 'Unknown'}</span>
              </div>
            </div>
            <div class="profile-actions">
              <button class="action-btn primary" on:click={startEditing}>Edit Profile</button>
            </div>
          {/if}
        </div>
      </div>

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

        <!-- Security Settings -->
        <div class="section">
          <h3>🔐 Security Settings</h3>
          <div class="security-items">
            <div class="security-item">
              <div class="security-info">
                <h4>Encryption Keys</h4>
                <p>Your public and private encryption keys</p>
              </div>
              <button class="security-btn">Manage Keys</button>
            </div>
            <div class="security-item">
              <div class="security-info">
                <h4>Backup Keys</h4>
                <p>Download your private key backup</p>
              </div>
              <button class="security-btn">Download</button>
            </div>
            {#if biometricSupported}
              <div class="security-item">
                <div class="security-info">
                  <h4>Biometric Authentication</h4>
                  <p>{biometricEnabled ? 'Disable biometric authentication' : 'Enable biometric authentication for enhanced security'}</p>
                  {#if biometricSetupError}
                    <p class="error-text">{biometricSetupError}</p>
                  {/if}
                </div>
                {#if !biometricEnabled}
                  <button
                    class="security-btn"
                    on:click={handleEnableBiometric}
                    disabled={biometricSetupInProgress}
                  >
                    {biometricSetupInProgress ? 'Setting up...' : 'Enable'}
                  </button>
                {/if}
              </div>
            {/if}
          </div>
        </div>

        <!-- App Settings -->
        <div class="section">
          <h3>⚙️ App Settings</h3>
          <div class="settings-items">
            <div class="setting-item">
              <div class="setting-info">
                <h4>Notifications</h4>
                <p>Manage your notification preferences</p>
              </div>
              <label class="toggle">
                <input type="checkbox" checked>
                <span class="slider"></span>
              </label>
            </div>
            <div class="setting-item">
              <div class="setting-info">
                <h4>Dark Mode</h4>
                <p>Toggle between light and dark themes</p>
              </div>
              <label class="toggle">
                <input type="checkbox" checked>
                <span class="slider"></span>
              </label>
            </div>
            <div class="setting-item">
              <div class="setting-info">
                <h4>Auto-lock</h4>
                <p>Automatically lock after inactivity</p>
              </div>
              <label class="toggle">
                <input type="checkbox">
                <span class="slider"></span>
              </label>
            </div>
          </div>
        </div>

        <!-- Actions -->
        <div class="section">
          <h3>🚪 Account Actions</h3>
          <div class="action-buttons">
            <button class="action-btn primary">Export Data</button>
            <button class="action-btn danger">Delete Account</button>
          </div>
        </div>
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="profile" />
    </div>
  </Auth>
{/if}

<style>
  .profile-container {
    min-height: 100vh;
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
    padding: 2rem;
    text-align: center;
    margin-bottom: 2rem;
  }

  .profile-avatar {
    position: relative;
    display: inline-block;
    margin-bottom: 1rem;
  }

  .avatar-large {
    width: 80px;
    height: 80px;
    border-radius: 50%;
    object-fit: cover;
    border: 2px solid var(--border);
    margin: 0 auto;
  }

  .edit-avatar-btn {
    position: absolute;
    bottom: 0;
    right: 0;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    border-radius: 50%;
    width: 24px;
    height: 24px;
    font-size: 0.8rem;
    cursor: pointer;
  }

  .profile-info h2 {
    text-align: left;
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.5rem;
  }

  .user-id {
    margin: 0 0 1rem 0;
    color: var(--muted);
    font-family: monospace;
    font-size: 0.9rem;
  }


  .profile-details {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    padding: 1rem;
    background: var(--input-bg);
    border-radius: 8px;
  }

  .detail-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .detail-item .label {
    color: var(--muted);
    font-size: 0.8rem;
    font-weight: 500;
  }

  .detail-item .value {
    color: var(--fg);
    font-size: 0.9rem;
  }

  .profile-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 1rem;
    justify-content: center;
  }

  .action-btn {
    padding: 0.5rem 1rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.9rem;
    transition: all 0.2s ease;
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
    gap: 1.5rem;
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


  .value.status-enabled {
    color: #4caf50;
    font-weight: 600;
  }

  .value.status-disabled {
    color: #ff9800;
    font-weight: 600;
  }

  .value.status-unsupported {
    color: #9e9e9e;
    font-weight: 600;
  }

  .error-text {
    color: #f44336;
    font-size: 0.75rem;
    margin: 0.25rem 0 0 0;
  }

  .profile-header {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }

  .avatar-container {
    flex-shrink: 0;
  }

  .profile-avatar {
    width: 80px;
    height: 80px;
    border-radius: 50%;
    object-fit: cover;
    border: 2px solid var(--border);
  }

  .profile-info {
    flex: 1;
    min-width: 0;
  }

  .user-bio {
    margin: 0.75rem 0 0 0;
    text-align: left;
    font-size: 0.8rem;
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
    transition: border-color 0.2s ease;
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

  .security-items, .settings-items {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .security-item, .setting-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    background: var(--input-bg);
    border-radius: 8px;
  }

  .security-info, .setting-info h4 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 0.9rem;
  }

  .security-info p, .setting-info p {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .security-btn {
    background: var(--primary);
    color: var(--button-text);
    border: none;
    padding: 0.5rem 1rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.8rem;
  }

  .security-btn.danger {
    background: var(--error);
    color: white;
  }

  .security-btn.danger:hover {
    background: #d32f2f;
  }

  .security-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .toggle {
    position: relative;
    display: inline-block;
    width: 44px;
    height: 24px;
  }

  .toggle input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    position: absolute;
    cursor: pointer;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: var(--border);
    transition: 0.2s;
    border-radius: 24px;
  }

  .slider:before {
    position: absolute;
    content: "";
    height: 18px;
    width: 18px;
    left: 3px;
    bottom: 3px;
    background-color: white;
    transition: 0.2s;
    border-radius: 50%;
  }

  input:checked + .slider {
    background-color: var(--primary);
  }

  input:checked + .slider:before {
    transform: translateX(20px);
  }

  .action-buttons {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .action-btn {
    padding: 0.75rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    font-weight: 600;
    transition: all 0.2s ease;
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

  /* Responsive Design */
  @media (max-width: 768px) {
    .profile-card {
      padding: 1rem;
    }

    .profile-content {
      padding: 0.5rem;
    }

    .security-item, .setting-item {
      flex-direction: column;
      align-items: flex-start;
      gap: 0.75rem;
    }

    .storage-stats {
      grid-template-columns: 1fr;
      gap: 0.75rem;
    }

    .storage-item {
      text-align: left;
    }

    .profile-details {
      padding: 0.75rem;
    }

    .detail-item {
      flex-direction: column;
      align-items: flex-start;
      gap: 0.25rem;
    }

    .profile-actions {
      flex-direction: column;
      gap: 0.5rem;
    }

    .action-btn {
      width: 100%;
      border-color: var(--border);
    }

  }
</style>
