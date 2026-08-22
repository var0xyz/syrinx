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
  import { resolveThreadId } from '$lib/utils/reedRef';
  import { goto } from '$app/navigation';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import MentionPicker from '$lib/components/MentionPicker.svelte';
  import { get } from 'svelte/store';
  import { isOnline } from '$lib/services/pwa';

  /** @type {boolean} */
  export let open = false;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let replyingTo = null;

  /** @type {import('$lib/types/reed').ReedType | null} */
  export let echoOf = null;

  const dispatch = createEventDispatcher();

  // Pin targets on the moment the modal opens (not for as long as it stays
  // open) so same-route navigation (echo/reply from detail) can't retarget
  // Quote at the newly created reed while we're still navigating to it —
  // `replyingTo`/`echoOf` are bound to the parent's `reed`, which is
  // reassigned by `applyPageData` as soon as the destination route's data
  // arrives, while this modal is still open and mid-close.
  let pinnedReply = null;
  let pinnedEcho = null;
  let wasOpen = false;
  $: {
    if (open && !wasOpen) {
      pinnedReply = replyingTo;
      pinnedEcho = echoOf;
    } else if (!open) {
      pinnedReply = null;
      pinnedEcho = null;
    }
    wasOpen = open;
  }

  /** @param {import('$lib/types/reed').ReedType} target */
  function refFor(target) {
    return target.id;
  }

  let content = '';
  let draftSaved = false;
  let saveDraftTimeout;
  let errorMessage = '';
  let isPublishing = false;
  let hasPendingRevocation = false;
  let textareaEl;
  let mentionPicker;
  /** userID -> username for mentions picked this session (preview hint only). */
  let mentionUsernameHints = new Map();

  $: if (open) checkPendingRevocation();

  async function checkPendingRevocation() {
    const fingerprint = authService.getActiveKeyFingerprint();
    if (!fingerprint) return;
    hasPendingRevocation = !!(await pendingRevocationRepository.get(fingerprint));
  }

  $: title = pinnedReply ? 'Reply Reed' : pinnedEcho ? 'Echo Reed' : 'New Reed';
  $: placeholder = pinnedReply ? "Write your reply" : pinnedEcho ? "Comment on it (Optional)" : "What's on your mind?";
  $: contentRequired = !pinnedEcho;

  $: characterCount = countMarkdownCharacters(content);
  $: characterLimit = MAX_REED_VISIBLE_CHARS;
  $: rawCharacterCount = content.length;
  $: rawCharacterLimit = MAX_REED_RAW_CHARS;
  $: isOverVisibleLimit = characterCount > characterLimit;
  $: isOverRawLimit = rawCharacterCount > rawCharacterLimit;
  $: isOverLimit = isOverVisibleLimit || isOverRawLimit;

  // Load draft when opened as a new reed only
  $: if (open && !pinnedReply && !pinnedEcho) {
    content = localStorage.getItem('reedDraft') || '';
  }

  function handleContentChange() {
    if (saveDraftTimeout) clearTimeout(saveDraftTimeout);
    saveDraftTimeout = setTimeout(() => {
      if (!pinnedReply) localStorage.setItem('reedDraft', content);
      draftSaved = true;
      setTimeout(() => { draftSaved = false; }, 2000);
    }, 1500);
  }

  function clear() {
    content = '';
    draftSaved = false;
    errorMessage = '';
    mentionUsernameHints = new Map();
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
      if (pinnedReply) {
        reed.replying = refFor(pinnedReply);
        reed.threadId = resolveThreadId(pinnedReply);
      }
      if (pinnedEcho) {
        reed.echoing = refFor(pinnedEcho);
      }
      const detachedArmor = await cryptoService.signMessage(reed.asMarkdown(), keyData.armor, passphrase);
      reed.setUserSignature(fingerprint, detachedArmor);
      const { publish } = await reedsService.createReed(reed);
      const href = `/reed/${reed.id}`;
      // Keep the modal open (covering the feed/detail page underneath) until
      // the new route is ready — on a slow connection, loading the detail
      // route's JS chunk can take a few seconds, and closing first flashes
      // the page behind the modal for that whole time. Pinned targets above
      // keep this safe even when echo/reply is on the reed detail route.
      await goto(href);
      close();
      publish.then((ok) => {
        if (!ok) {
          const message = get(isOnline)
            ? "There was an issue with the server. Your reed will be published automatically once it's resolved."
            : "You're offline. We'll publish this reed as soon as you're back online.";
          notificationStore.info(message, 10000);
        }
      });
    } catch (error) {
      console.error('Error publishing reed:', error);
      errorMessage = error.message || 'Failed to publish reed';
      notificationStore.error(errorMessage);
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

    {#if pinnedReply}
      <Quote reed={pinnedReply} type="reply" maxLines={4} />
    {:else if pinnedEcho}
      <Quote reed={pinnedEcho} type="echo" maxLines={4} />
    {/if}

    <form on:submit|preventDefault={publish}>
      <div class="form-group">
        <textarea
          bind:this={textareaEl}
          placeholder={placeholder}
          rows="6"
          bind:value={content}
          on:input={() => { handleContentChange(); mentionPicker?.handleCaretChange(); }}
          on:click={() => mentionPicker?.handleCaretChange()}
          on:keyup={(e) => {
            if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) mentionPicker?.handleCaretChange();
          }}
        ></textarea>
        <MentionPicker bind:this={mentionPicker} textarea={textareaEl} bind:content bind:usernameHints={mentionUsernameHints} />
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
      <div class="reed-preview">
        <div class="reed-preview-label">Preview</div>
        <div class="reed-preview-card">
          <div class="reed-preview-body">
            {#if content.trim()}
              <MarkdownParser text={content} preview={true} usernameHints={mentionUsernameHints} />
            {:else}
              <p class="reed-preview-empty">Your reed will appear here as you type.</p>
            {/if}
          </div>
        </div>
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

  .reed-preview {
    margin-top: 1.25rem;
  }

  .reed-preview-label {
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
    margin-bottom: 0.5rem;
  }

  .reed-preview-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
  }

  .reed-preview-body {
    padding: 1.25rem 1.5rem;
    color: var(--fg);
  }

  .reed-preview-empty {
    margin: 0;
    color: var(--muted);
    font-size: 0.95rem;
    line-height: 1.4;
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
