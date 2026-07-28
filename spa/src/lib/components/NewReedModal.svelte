<script>
  import { createEventDispatcher } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
  import { reedsService } from '$lib/repositories/reeds';
  import {
    MAX_REED_RAW_CHARS,
    MAX_REED_VISIBLE_CHARS,
    countMarkdownCharacters,
    reedContentWithinLimits,
  } from '$lib/utils/reedContent';
  import { notificationStore } from '$lib/stores/notifications';
  import { Reed } from '$lib/types/reed';
  import { formatReedRef } from '$lib/utils/reedRef';
  import { goto } from '$app/navigation';
  import Quote from '$lib/components/Quote.svelte';

  /** @type {boolean} */
  export let open = false;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let replyingTo = null;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let echoOf = null;

  const dispatch = createEventDispatcher();

  /** @param {import('$lib/types/reed').ReedType} target */
  function refFor(target) {
    const serverId = target.serverSignature?.serverID || localStorage.getItem('serverId') || '';
    if (!serverId) throw new Error('Server ID not available');
    return formatReedRef(target.userID, serverId, target.id);
  }

  let content = '';
  let draftSaved = false;
  let saveDraftTimeout;
  let errorMessage = '';
  let isPublishing = false;
  let hasPendingRevocation = false;

  $: if (open) checkPendingRevocation();

  async function checkPendingRevocation() {
    const fingerprint = authService.getActiveKeyFingerprint();
    if (!fingerprint) return;
    hasPendingRevocation = !!(await pendingRevocationRepository.get(fingerprint));
  }

  $: title = replyingTo ? 'Reply Reed' : echoOf ? 'Echo Reed' : 'New Reed';
  $: placeholder = replyingTo ? "Write your reply" : echoOf ? "Comment on it (Optional)" : "What's on your mind?";
  $: contentRequired = !echoOf;

  $: characterCount = countMarkdownCharacters(content);
  $: characterLimit = MAX_REED_VISIBLE_CHARS;
  $: rawCharacterCount = content.length;
  $: rawCharacterLimit = MAX_REED_RAW_CHARS;
  $: isOverVisibleLimit = characterCount > characterLimit;
  $: isOverRawLimit = rawCharacterCount > rawCharacterLimit;
  $: isOverLimit = isOverVisibleLimit || isOverRawLimit;

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

  function clear() {
    content = '';
    draftSaved = false;
    errorMessage = '';
    if (saveDraftTimeout) clearTimeout(saveDraftTimeout);
    localStorage.removeItem('reedDraft');
  }

  function close() {
    clear();
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

      if (hasPendingRevocation) {
        errorMessage = 'Your key is pending revocation. Publishing is disabled.';
        return;
      }

      if (contentRequired && !content.trim()) {
        errorMessage = 'Cannot publish empty reed.';
        return;
      }

      if (!reedContentWithinLimits(content)) {
        if (content.length > MAX_REED_RAW_CHARS) {
          errorMessage = `Message is too long (${content.length}/${MAX_REED_RAW_CHARS} characters).`;
        } else {
          errorMessage = `Message is too long (${countMarkdownCharacters(content)}/${MAX_REED_VISIBLE_CHARS} characters).`;
        }
        return;
      }

      const fingerprint = authService.getActiveKeyFingerprint();
      const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
      if (!keyData) throw new Error('Private key not found. Please import your key.');

      const passphrase = authService.getPassphrase();
      if (!passphrase) throw new Error('PANIC: No passphrase found.');

      const reed = new Reed();
      reed.content = content;
      if (replyingTo) {
        reed.replying = refFor(replyingTo);
      }
      if (echoOf) {
        reed.echoing = refFor(echoOf);
      }
      const detachedArmor = await cryptoService.signMessage(reed.asMarkdown(), keyData.armor, passphrase);
      reed.setUserSignature(fingerprint, detachedArmor);
      const published = await reedsService.createReed(reed);
      if (published) {
        goto(`/reed/${user.id}/${reed.id}`);
      } else {
        notificationStore.info("There was an issue with the server. Your reed will be published automatically once it's resolved.", 10000);
      }
      close();
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
      <Quote reed={replyingTo} type="reply" maxLines={4} />
    {:else if echoOf}
      <Quote reed={echoOf} type="echo" maxLines={4} />
    {/if}

    <form on:submit|preventDefault={publish}>
      <div class="form-group">
        <textarea
          placeholder={placeholder}
          rows="6"
          bind:value={content}
          on:input={handleContentChange}
        ></textarea>
        <div class="content-info">
          <div class="draft-saved" class:hidden={!draftSaved}>Draft saved</div>
          <div class="character-counter" class:over-limit={isOverVisibleLimit}>
            {characterCount}/{characterLimit} characters
          </div>
        </div>
        {#if isOverRawLimit}
          <div class="error-message">
            Message is too long ({rawCharacterCount}/{rawCharacterLimit} characters).
          </div>
        {/if}
      </div>
      {#if hasPendingRevocation}
        <div class="revocation-warning">
          ⚠️ Your encryption key is being revoked. Publishing is disabled until the revocation is complete.
        </div>
      {/if}
      {#if errorMessage}
        <div class="error-message">{errorMessage}</div>
      {/if}
      <div class="form-actions">
        <button type="button" class="btn btn-secondary" on:click={close} disabled={isPublishing}>Discard</button>
        <button type="submit" class="btn btn-primary" disabled={isPublishing || isOverLimit || hasPendingRevocation || (contentRequired && !content.trim())}>
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

  .form-group {
    margin-top: 1rem;
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

  .revocation-warning {
    background: #fffbe6;
    border: 1px solid #ffe58f;
    border-radius: 8px;
    color: #7c5c00;
    padding: 0.75rem;
    margin: 0.5rem 0;
    font-size: 0.9rem;
    line-height: 1.4;
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
