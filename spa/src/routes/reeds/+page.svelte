<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { reedsService, countMarkdownCharacters, stripMarkdown, formatRelativeTime } from '$lib/repositories/reeds';
  import { Reed } from '$lib/types/reed';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import { goto } from '$app/navigation';

  let user = null;
  let loading = true;
  let isWriteSectionOpen = false;
  let content = '';
  let draftSaved = false;
  let saveDraftTimeout;
  let errorMessage = '';
  let isPublishing = false;

  // Reed list state
  let reeds = [];
  let loadingReeds = true;
  let errorLoadingReeds = '';

  // Character counter
  $: characterCount = countMarkdownCharacters(content);
  $: characterLimit = 140;
  $: isOverLimit = characterCount > characterLimit;

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();

      if (!user) {
        // Redirect to home page if no user
        window.location.href = '/';
        return;
      }

      // Load draft from localStorage
      loadDraft();

      // Load user's reeds
      await loadReeds();

    } catch (error) {
      console.error('Error getting user:', error);
      // Redirect to home page on error
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });

  function loadDraft() {
    content = localStorage.getItem('reedDraft') || '';
  }

  async function loadReeds() {
    try {
      loadingReeds = true;
      errorLoadingReeds = '';
      reeds = await reedsService.getReedsByAuthor(user.id);
    } catch (error) {
      console.error('Error loading reeds:', error);
      errorLoadingReeds = 'Failed to load reeds';
    } finally {
      loadingReeds = false;
    }
  }

  function saveDraft() {
    localStorage.setItem('reedDraft', content);
  }

  function toggleWriteSection() {
    isWriteSectionOpen = !isWriteSectionOpen;
    if (!isWriteSectionOpen) {
      // Clear form when closing
      content = '';
      draftSaved = false;
    }
  }

  function handleContentChange() {
    // Clear existing timeout
    if (saveDraftTimeout) {
      clearTimeout(saveDraftTimeout);
    }

    // Set new timeout to save after 1.5 seconds of no typing
    // This ensures changes are always saved, even if made while "Draft saved" message is visible
    saveDraftTimeout = setTimeout(() => {
      // Auto-save logic
      saveDraft();
      draftSaved = true;
      setTimeout(() => {
        draftSaved = false;
      }, 2000); // Hide the message after 2 seconds
    }, 1500);
  }

  function discardDraft() {
    content = '';
    draftSaved = false;
    if (saveDraftTimeout) {
      clearTimeout(saveDraftTimeout);
    }
    // Clear draft from localStorage
    localStorage.removeItem('reedDraft');
    isWriteSectionOpen = false;
  }

  async function getPrivateKeyAndPassphrase() {
    // Get the active key fingerprint
    const fingerprint = authService.getActiveKeyFingerprint();
    if (!fingerprint) {
      throw new Error('No active key found. Please set an active key in your profile.');
    }

    // Get the private key from IndexedDB
    const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
    if (!keyData) {
      throw new Error('Private key not found. Please import your key.');
    }

    const passphrase = authService.getPassphrase();
    if (!passphrase) {
      throw new Error('PANIC: No passphrase found.');
    }

    return { keyData, passphrase };
  }

  async function publishReed() {
    if (isPublishing) return; // Prevent multiple simultaneous publishes

    await performPublish();
  }

  async function performPublish() {
    if (isPublishing) return; // Prevent multiple simultaneous publishes

    isPublishing = true;
    errorMessage = '';

    try {
      const user = await authService.getCurrentUser();
      if (!user) {
        errorMessage = 'No user ID found. Please log in.';
        throw new Error(errorMessage);
      }

      // Get active key fingerprint from localStorage
      const activeKeyFingerprint = authService.getActiveKeyFingerprint();
      if (!activeKeyFingerprint) {
        errorMessage = 'No active key fingerprint found.';
        throw new Error(errorMessage);
      }
      console.log("Using fingerprint:", activeKeyFingerprint);

      // Check if content is not empty
      if (!content.trim()) {
        errorMessage = 'Cannot publish empty reed.';
        throw new Error(errorMessage);
      }

      // Create new Reed instance
      const reed = new Reed();
      reed.content = content;
      reed.fingerprint = activeKeyFingerprint;

      console.log('Signing reed content...');

      // Get private key and passphrase - let authentication errors propagate
      const { keyData, passphrase } = await getPrivateKeyAndPassphrase();

      // Sign the content using the crypto service
      reed.signature = await cryptoService.signMessage(reed.asMarkdown(), keyData.armor, passphrase);

      // Why are we using markdown here? Because we need a reliable way to
      // represent the reed, a way that doesn't rely on language implementation
      // details such as a javascript object. That way we can be sure of what we
      // are signing, and that it can be reproduced and verified by anyone.
      const signedMarkdown = reed.asMarkdown();

      // Send reed instance to server for validation and storage
      console.log('Sending reed to server...');
      await reedsService.createReed(reed);
      console.log('Reed published successfully!');

      // Clear form after publishing
      content = '';
      draftSaved = false;
      // Clear draft from localStorage after publishing
      localStorage.removeItem('reedDraft');
      isWriteSectionOpen = false;

      // Redirect to the newly created reed
      goto(`/reed/${user.id}/${reed.id}`);

      // ████████╗ ██████╗ ██████╗  ██████╗
      // ╚══██╔══╝██╔═══██╗██╔══██╗██╔═══██╗
      //    ██║   ██║   ██║██║  ██║██║   ██║
      //    ██║   ██║   ██║██║  ██║██║   ██║
      //    ██║   ╚██████╔╝██████╔╝╚██████╔╝
      //    ╚═╝    ╚═════╝ ╚═════╝  ╚═════╝
      // =====================================
      //    Handle Storage Quota Gracefully
      // =====================================
      // - [ ] Check available space on device before publishing
      // - [ ] Show a warning if the device is low on space
      // - [ ] Show an error if we didn't have enough space to publish the reed

    } catch (error) {
      console.error('Error publishing reed:', error);
      errorMessage = error.message || 'Failed to publish reed';
    } finally {
      isPublishing = false;
    }
  }

  async function deleteReed(reedId) {
    if (!confirm('Are you sure you want to delete this reed?')) {
      return;
    }

    await performDelete(reedId);
  }

  async function performDelete(reedId) {
    try {
      // Delete from server
      await apiService.deleteReed(user.id, reedId);

      // Delete from IndexedDB
      await dbService.delete('reeds', reedId);

      // Remove from local state
      reeds = reeds.filter(reed => reed.headers.id !== reedId);
    } catch (error) {
      console.error('Error deleting reed:', error);
      errorMessage = 'Failed to delete reed';
    }
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.headers.author}/${reed.headers.id}`);
  }

</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading reeds...</h2>
        <p>Please wait while we fetch your message threads.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="reeds-container">
    <!-- Floating Write Button -->
    <button class="floating-write-btn" on:click={toggleWriteSection}>
      <span class="icon">✍️</span>
    </button>

    <!-- Write Section -->
    {#if isWriteSectionOpen}
      <div class="write-section-container">
        <div class="write-section" class:hidden={!isWriteSectionOpen}>
          <div class="write-form">
            <form on:submit|preventDefault={publishReed}>
              <div class="form-group">
                <textarea
                  id="content"
                  placeholder="What's on your mind?"
                  rows="6"
                  bind:value={content}
                  on:input={handleContentChange}
                ></textarea>
                <div class="content-info">
                  <div class="draft-saved" class:hidden={!draftSaved}>Draft saved</div>
                  <div class="character-counter" class:over-limit={isOverLimit}>
                    {characterCount}/{characterLimit} characters
                  </div>
              </div>
              </div>
              {#if errorMessage}
                <div class="error-message">
                  {errorMessage}
                </div>
              {/if}
              <div class="form-actions">
                <button type="button" class="btn btn-secondary" on:click={discardDraft} disabled={isPublishing}>Discard</button>
                <button type="submit" class="btn btn-primary" disabled={isPublishing || !content.trim()}>
                  {isPublishing ? 'Publishing...' : 'Publish'}
                </button>
              </div>
            </form>
          </div>
        </div>
      </div>
    {/if}

    <!-- Main Content -->
    <div class="reeds-content">
      <div class="reeds-list">
        {#if loadingReeds}
          <div class="loading">
            <h2>Loading reeds...</h2>
            <p>Please wait while we fetch your reeds.</p>
          </div>
        {:else if errorLoadingReeds}
          <div class="error-state">
            <div class="error-icon">⚠️</div>
            <h3>Error loading reeds</h3>
            <p>{errorLoadingReeds}</p>
            <button class="btn btn-primary" on:click={loadReeds}>Try Again</button>
          </div>
        {:else if reeds.length === 0}
          <div class="empty-state">
            <div class="empty-icon">🌾</div>
            <h3>No reeds yet</h3>
            <p>Your reeds will appear here when you publish them.</p>
          </div>
        {:else}
          {#each reeds as reed (reed.headers.id)}
            <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(reed)}>
              <div class="reed-header">
                <div class="reed-info">
                  <div class="reed-avatar">
                    {#if user.avatarURL}
                      <img src={user.avatarURL} alt={user.username} />
                    {:else}
                      <div class="reed-icon">👤</div>
                    {/if}
                  </div>
                  <div class="reed-details">
                    <h3>{user.username}</h3>
                    <p>{formatRelativeTime(reed.headers.timestamp)}</p>
                  </div>
                </div>
                <div class="reed-meta">
                  <button class="reed-menu" on:click|stopPropagation={() => deleteReed(reed.headers.id)}>
                    <span class="menu-dots">🗑️</span>
                  </button>
                </div>
              </div>
              <div class="reed-preview">
                <p>{stripMarkdown(reed.content)}</p>
              </div>
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <!-- Bottom Toolbar -->
    <BottomToolbar currentPage="reeds" />
    </div>
  </Auth>
{/if}

<style>
  .reeds-container {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }


  .floating-write-btn {
    position: fixed;
    bottom: 80px;
    right: 20px;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
  }

  .floating-write-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-write-btn .icon {
    font-size: 1.5rem;
  }

  .write-section-container {
    padding: 0.5rem;
  }

  .write-section {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    margin: 0 auto;
    max-width: 700px;
    width: 100%;
    padding: 1.5rem;
    animation: slideDown 0.3s ease;
  }

  @keyframes slideDown {
    from {
      opacity: 0;
      transform: translateY(-20px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .form-group textarea {
    width: 100%;
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    resize: vertical;
    min-height: 120px;
  }

  .content-info {
    display: flex;
    justify-content: space-between;
   }

  .draft-saved {
    color: var(--primary);
    font-size: 0.8rem;
    margin-top: 0.5rem;
    opacity: 0.8;
    min-height: 1rem;
    transition: opacity 0.3s ease, transform 0.3s ease;
  }

  .draft-saved.hidden {
    opacity: 0;
    transition: opacity 0.3s ease, transform 0.3s ease;
  }

  .character-counter {
    text-align: right;
    font-size: 0.8rem;
    color: var(--muted);
    margin-top: 0.5rem;
    min-height: 1rem;
    transition: color 0.2s ease;
  }

  .character-counter.over-limit {
    color: #ff6b6b;
  }

  .error-message {
    color: #ff6b6b;
    background: #ffe0e0;
    border: 1px solid #ffb3b3;
    border-radius: 8px;
    padding: 0.75rem;
    margin: 0.5rem 0;
    font-size: 0.9rem;
    line-height: 1.4;
  }

  .form-actions {
    display: flex;
    gap: 1rem;
    justify-content: space-between;
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    transition: all 0.2s ease;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--border);
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .btn:disabled:hover {
    opacity: 0.6;
    transform: none;
  }

  .reeds-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .reeds-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .reed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
    cursor: pointer;
  }

  .reed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .reed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-info {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .reed-icon {
    width: 40px;
    height: 40px;
    border-radius: 8px;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.2rem;
  }

  .reed-avatar {
    width: 40px;
    height: 40px;
    border-radius: 8px;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--input-bg);
  }

  .reed-avatar img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .reed-menu {
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.25rem;
    border-radius: 4px;
    transition: background-color 0.2s ease;
  }

  .reed-menu:hover {
    background: var(--border);
  }

  .menu-dots {
    font-size: 1.2rem;
    color: var(--muted);
  }

  .error-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .error-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .error-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .error-state p {
    margin: 0 0 1rem 0;
    font-size: 0.9rem;
  }

  .reed-details h3 {
    margin: 0 0 0.25rem 0;
    color: var(--fg);
    font-size: 1rem;
    font-weight: 600;
  }

  .reed-details p {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .reed-meta {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
  }

  .reed-preview p {
    margin: 0;
    line-height: 1.4;
    font-size: 0.9rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
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
    .reeds-content {
      padding: 0.5rem;
    }

    .reed-header {
      padding: 0.75rem;
    }

    .reed-preview {
      padding: 0.75rem;
    }

  }
</style>
