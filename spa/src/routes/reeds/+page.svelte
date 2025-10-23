<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { sessionStore } from '$lib/stores/session';
  import { reedsService } from '$lib/services/reeds';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';

  let user = null;
  let loading = true;
  let isWriteSectionOpen = false;
  let title = '';
  let content = '';
  let draftSaved = false;
  let saveTimeout;
  let errorMessage = '';
  let isPublishing = false;

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
    } catch (error) {
      console.error('Error getting user:', error);
      // Redirect to home page on error
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });

  function loadDraft() {
    try {
      const savedDraft = localStorage.getItem('reed-draft');
      if (savedDraft) {
        const draft = JSON.parse(savedDraft);
        title = draft.title || '';
        content = draft.content || '';
      }
    } catch (error) {
      console.error('Error loading draft:', error);
    }
  }

  function saveDraft() {
    try {
      const draft = { title, content };
      localStorage.setItem('reed-draft', JSON.stringify(draft));
    } catch (error) {
      console.error('Error saving draft:', error);
    }
  }

  function toggleWriteSection() {
    isWriteSectionOpen = !isWriteSectionOpen;
    if (!isWriteSectionOpen) {
      // Clear form when closing
      title = '';
      content = '';
      draftSaved = false;
    }
  }

  function handleContentChange() {
    // Clear existing timeout
    if (saveTimeout) {
      clearTimeout(saveTimeout);
    }

    // Only show message if it's not already visible
    if (!draftSaved) {
      // Set new timeout to save after 200ms of no typing
      saveTimeout = setTimeout(() => {
        // Auto-save logic
        saveDraft();
        draftSaved = true;
        setTimeout(() => {
          draftSaved = false;
        }, 2000); // Hide the message after 2 seconds
      }, 1500);
    }
  }

  function discardDraft() {
    title = '';
    content = '';
    draftSaved = false;
    if (saveTimeout) {
      clearTimeout(saveTimeout);
    }
    // Clear draft from localStorage
    localStorage.removeItem('reed-draft');
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

    // Get passphrase from session store
    const passphrase = sessionStore.get('passphrase');
    if (!passphrase) {
      throw new Error('Authentication required. Please enter your passphrase.');
    }

    return { keyData, passphrase };
  }

  async function publishReed() {
    if (isPublishing) return; // Prevent multiple simultaneous publishes

    isPublishing = true;
    errorMessage = '';

    const user = await authService.getCurrentUser();
    if (!user) {
      errorMessage = 'No user ID found. Please log in.';
      throw new Error(errorMessage);
    }

    // Get active key fingerprint from localStorage
    const activeKeyFingerprint = privateKeyRepository.getActiveKeyFingerprint();
    if (!activeKeyFingerprint) {
      errorMessage = 'No active key fingerprint found.';
      throw new Error(errorMessage);
    }

    // Check if content is not empty
    if (!content.trim()) {
      errorMessage = 'Cannot publish empty reed.';
      throw new Error(errorMessage);
    }

    // Build markdown content with required header fields
    const markdownContent = `---
origin: ${window.location.origin}
author: ${user.id}
key: ${activeKeyFingerprint}
algorithm: PGP
---
${content}`;

      console.log('Signing reed content...');

      // Get private key and passphrase
      const { keyData, passphrase } = await getPrivateKeyAndPassphrase();

      // Sign the content using the crypto service
      const signature = await cryptoService.signMessage(markdownContent, keyData.armor, passphrase);

      // Clean up signature for header - remove PGP markers and escape newlines
      const rawSignature = signature
        .replace(/-----BEGIN PGP SIGNATURE-----\n\n/g, '')
        .replace(/\n-----END PGP SIGNATURE-----/g, '')
      const escapedSignature = rawSignature.replace(/\n/g, '\\n');

      // Add signature to the markdown header
      const signedMarkdown = `---
origin: ${window.location.origin}
author: ${user.id}
key: ${activeKeyFingerprint}
algorithm: PGP
signature: ${escapedSignature}
---
${content}`;

      console.log('Publishing signed reed as markdown:');
      console.log(signedMarkdown);

      // Send only the signature to server for validation and storage
      console.log('Sending signature to server...');
      const response = await reedsService.createReedWithContent(signedMarkdown, rawSignature);
      console.log('Reed published successfully!', response);
      if (response.success) {
        console.log('Reed published successfully!', response.reed);
      } else {
        throw new Error(response.message || 'Failed to publish reed');
      }

      // Clear form after publishing
      title = '';
      content = '';
      draftSaved = false;
      // Clear draft from localStorage after publishing
      localStorage.removeItem('reed-draft');
      isWriteSectionOpen = false;

    isPublishing = false;
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
            <div class="draft-saved" class:hidden={!draftSaved}>Draft saved</div>
            </div>
            {#if errorMessage}
              <div class="error-message">
                {errorMessage}
              </div>
            {/if}
            <div class="form-actions">
              <button type="button" class="btn btn-secondary" on:click={discardDraft} disabled={isPublishing}>Discard</button>
              <button type="submit" class="btn btn-primary" disabled={isPublishing || !content.trim()}>
                {isPublishing ? 'Publishing...' : 'Publish Reed'}
              </button>
            </div>
          </form>
        </div>
      </div>
    {/if}

    <!-- Main Content -->
    <div class="reeds-content">
      <div class="reeds-list">
        <!-- Reed Item 1 -->
        <div class="reed-item">
          <div class="reed-header">
            <div class="reed-info">
              <div class="reed-icon">📝</div>
              <div class="reed-details">
                <h3>Welcome Thread</h3>
                <p>System messages and onboarding</p>
              </div>
            </div>
            <div class="reed-meta">
              <span class="reed-time">2 hours ago</span>
              <span class="reed-status unread">3</span>
            </div>
          </div>
          <div class="reed-preview">
            <p>Welcome to Syrinx! This thread contains important information about getting started with secure messaging.</p>
          </div>
        </div>

        <!-- Reed Item 2 -->
        <div class="reed-item">
          <div class="reed-header">
            <div class="reed-info">
              <div class="reed-icon">🔐</div>
              <div class="reed-details">
                <h3>Security Updates</h3>
                <p>Important security notifications</p>
              </div>
            </div>
            <div class="reed-meta">
              <span class="reed-time">1 day ago</span>
              <span class="reed-status read">0</span>
            </div>
          </div>
          <div class="reed-preview">
            <p>Your encryption keys have been successfully generated and are ready for use.</p>
          </div>
        </div>

        <!-- Reed Item 3 -->
        <div class="reed-item">
          <div class="reed-header">
            <div class="reed-info">
              <div class="reed-icon">🌐</div>
              <div class="reed-details">
                <h3>Network Status</h3>
                <p>Connection and network updates</p>
              </div>
            </div>
            <div class="reed-meta">
              <span class="reed-time">3 days ago</span>
              <span class="reed-status read">0</span>
            </div>
          </div>
          <div class="reed-preview">
            <p>You're now connected to the Syrinx secure messaging network.</p>
          </div>
        </div>

        <!-- Empty State -->
        <div class="empty-state">
          <div class="empty-icon">🌾</div>
          <h3>No more reeds</h3>
          <p>You're all caught up! New message threads will appear here.</p>
        </div>
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

  .write-section {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    margin: 0.5rem;
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

  .write-form h3 {
    margin: 0 0 1.5rem 0;
    color: var(--fg);
    font-size: 1.2rem;
  }

  .form-group label {
    display: block;
    margin-bottom: 0.5rem;
    color: var(--fg);
    font-weight: 500;
  }

  .form-group input,
  .form-group textarea {
    width: 100%;
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
  }

  .form-group textarea {
    resize: vertical;
    min-height: 120px;
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
    gap: 1rem;
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

  .reed-time {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .reed-status {
    padding: 0.25rem 0.5rem;
    border-radius: 12px;
    font-size: 0.75rem;
    font-weight: 600;
  }

  .reed-status.unread {
    background: var(--primary);
    color: var(--button-text);
  }

  .reed-status.read {
    background: var(--input-bg);
    color: var(--muted);
  }

  .reed-preview {
    padding: 1rem;
  }

  .reed-preview p {
    margin: 0;
    color: var(--muted);
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
