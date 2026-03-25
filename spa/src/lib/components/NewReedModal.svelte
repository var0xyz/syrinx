<script>
  import { createEventDispatcher } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { reedsService, countMarkdownCharacters, stripMarkdown, formatRelativeTime } from '$lib/repositories/reeds';
  import { Reed } from '$lib/types/reed';
  import { goto } from '$app/navigation';

  /** @type {boolean} */
  export let open = false;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let replyingTo = null;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let echoOf = null;

  const dispatch = createEventDispatcher();

  let content = '';
  let draftSaved = false;
  let saveDraftTimeout;
  let errorMessage = '';
  let isPublishing = false;

  $: title = replyingTo ? 'Reply' : echoOf ? 'Echo' : 'New Reed';
  $: contentRequired = !echoOf;

  $: characterCount = countMarkdownCharacters(content);
  $: characterLimit = 140;
  $: isOverLimit = characterCount > characterLimit;

  // Load draft when opened as a new reed only
  $: if (open && !replyingTo && !echoOf) {
    content = localStorage.getItem('reedDraft') || '';
  }

  function handleContentChange() {
    if (saveDraftTimeout) clearTimeout(saveDraftTimeout);
    saveDraftTimeout = setTimeout(() => {
      if (!replyingTo) localStorage.setItem('reedDraft', content);
      draftSaved = true;
      setTimeout(() => { draftSaved = false; }, 2000);
    }, 1500);
  }

  function close() {
    content = '';
    draftSaved = false;
    errorMessage = '';
    if (saveDraftTimeout) clearTimeout(saveDraftTimeout);
    if (!replyingTo && !echoOf) localStorage.removeItem('reedDraft');
    dispatch('close');
  }

  async function publish() {
    if (isPublishing) return;
    isPublishing = true;
    errorMessage = '';

    try {
      const user = await authService.getCurrentUser();
      if (!user) {
        errorMessage = 'No user ID found. Please log in.';
        return;
      }

      const activeKeyFingerprint = authService.getActiveKeyFingerprint();
      if (!activeKeyFingerprint) {
        errorMessage = 'No active key fingerprint found.';
        return;
      }

      if (contentRequired && !content.trim()) {
        errorMessage = 'Cannot publish empty reed.';
        return;
      }

      const fingerprint = authService.getActiveKeyFingerprint();
      const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
      if (!keyData) throw new Error('Private key not found. Please import your key.');

      const passphrase = authService.getPassphrase();
      if (!passphrase) throw new Error('PANIC: No passphrase found.');

      const reed = new Reed();
      reed.content = content;
      reed.fingerprint = activeKeyFingerprint;
      if (replyingTo) {
        reed.replying = replyingTo.headers.id;
      }
      if (echoOf) {
        reed.echoing = `${echoOf.headers.author}!${echoOf.headers.id}`;
      }

      reed.signature = await cryptoService.signMessage(reed.asMarkdown(), keyData.armor, passphrase);

      await reedsService.createReed(reed);

      localStorage.removeItem('reedDraft');
      dispatch('close');
      goto(`/reed/${user.id}/${reed.id}`);
    } catch (error) {
      console.error('Error publishing reed:', error);
      errorMessage = error.message || 'Failed to publish reed';
    } finally {
      isPublishing = false;
    }
  }
</script>

{#if open}
  <div class="modal-overlay" on:click={close} role="presentation"></div>
  <div class="write-modal">
    <div class="write-modal-header">
      <h2>{title}</h2>
      <button class="close-btn" on:click={close} aria-label="Close">✕</button>
    </div>

    {#if replyingTo}
      <div class="reply-preview">
        <div class="reply-preview-meta">{replyingTo.headers.author} · {formatRelativeTime(replyingTo.headers.timestamp)}</div>
        <div class="reply-preview-content">{stripMarkdown(replyingTo.content)}</div>
      </div>
    {:else if echoOf}
      <div class="reply-preview">
        <div class="reply-preview-meta">{echoOf.headers.author} · {formatRelativeTime(echoOf.headers.timestamp)}</div>
        <div class="reply-preview-content">{stripMarkdown(echoOf.content)}</div>
      </div>
    {/if}

    <form on:submit|preventDefault={publish}>
      <div class="form-group">
        <textarea
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
        <div class="error-message">{errorMessage}</div>
      {/if}
      <div class="form-actions">
        <button type="button" class="btn btn-secondary" on:click={close} disabled={isPublishing}>Discard</button>
        <button type="submit" class="btn btn-primary" disabled={isPublishing || (contentRequired && !content.trim())}>
          {isPublishing ? 'Publishing...' : 'Publish'}
        </button>
      </div>
    </form>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 1001;
    animation: fadeIn 0.25s ease;
  }

  .write-modal {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: 100%;
    background: var(--surface);
    z-index: 1002;
    display: flex;
    flex-direction: column;
    padding: 1.5rem;
    animation: slideInRight 0.3s ease;
    overflow-y: auto;
  }

  .write-modal-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 1.5rem;
  }

  .write-modal-header h2 {
    flex: 1;
    margin: 0;
    font-size: 1.25rem;
    color: var(--fg);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .close-btn {
    flex-shrink: 0;
    width: 2rem;
    height: 2rem;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: var(--muted);
    font-size: 1rem;
    cursor: pointer;
    border-radius: 6px;
    transition: color 0.2s ease, background 0.2s ease;
  }

  .close-btn:hover {
    color: var(--fg);
    background: var(--border);
  }

  .reply-preview {
    background: var(--bg);
    border: 1px solid var(--border);
    border-left: 3px solid var(--primary);
    border-radius: 8px;
    padding: 0.75rem 1rem;
    margin-bottom: 1rem;
  }

  .reply-preview-meta {
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
  }

  .reply-preview-content {
    font-size: 0.9rem;
    color: var(--fg);
    line-height: 1.4;
    white-space: pre-wrap;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
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
    box-sizing: border-box;
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
    transition: opacity 0.3s ease;
  }

  .draft-saved.hidden {
    opacity: 0;
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
    margin-top: 1rem;
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

  .btn-primary:hover { opacity: 0.9; }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover { background: var(--border); }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to   { opacity: 1; }
  }

  @keyframes slideInRight {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }
</style>
