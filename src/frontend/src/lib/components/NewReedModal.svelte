<script>
  import { createEventDispatcher, tick } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { requestSigner } from '$lib/services/request-signer';
  import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
  import { reedsService } from '$lib/repositories/reeds';
  import {
    MAX_REED_RAW_CHARS,
    MAX_REED_VISIBLE_CHARS,
    MAX_THREAD_REEDS,
    countMarkdownCharacters,
    reedContentWithinLimits,
  } from '$lib/utils/reedContent';
  import { notificationStore } from '$lib/stores/notifications';
  import { Reed } from '$lib/types/reed';
  import { resolveThreadId } from '$lib/utils/identityRef';
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
      if (!pinnedReply && !pinnedEcho) loadDraft();
    } else if (!open) {
      pinnedReply = null;
      pinnedEcho = null;
    }
    wasOpen = open;
  }

  let nextKey = 1;
  /** @type {{ key: number, text: string }[]} */
  let parts = [{ key: nextKey++, text: '' }];
  let draftSaved = false;
  let saveDraftTimeout;
  let errorMessage = '';
  let isPublishing = false;
  let hasPendingRevocation = false;
  /** @type {HTMLTextAreaElement[]} */
  let textareas = [];
  /** @type {any[]} */
  let mentionPickers = [];
  /** userID -> username for mentions picked this session (preview hint only). */
  let mentionUsernameHints = new Map();

  $: if (open) checkPendingRevocation();

  async function checkPendingRevocation() {
    const keyId = authService.getActiveKeyId();
    if (!keyId) return;
    hasPendingRevocation = !!(await pendingRevocationRepository.get(keyId));
  }

  // Threads chain new reeds only; replies and echoes stay single.
  $: threadable = !pinnedReply && !pinnedEcho;
  $: isThread = parts.length > 1;
  $: title = pinnedReply ? 'Reply Reed' : pinnedEcho ? 'Echo Reed' : isThread ? 'New Thread' : 'New Reed';
  $: placeholder = pinnedReply ? "Write your reply" : pinnedEcho ? "Comment on it (Optional)" : "What's on your mind?";
  $: contentRequired = !pinnedEcho;

  $: characterLimit = MAX_REED_VISIBLE_CHARS;
  $: rawCharacterLimit = MAX_REED_RAW_CHARS;
  $: counts = parts.map((p) => countMarkdownCharacters(p.text));
  $: overVisible = counts.map((c) => c > characterLimit);
  $: overRaw = parts.map((p) => p.text.length > rawCharacterLimit);
  $: isOverLimit = overVisible.some(Boolean) || overRaw.some(Boolean);
  $: anyEmpty = parts.some((p) => !p.text.trim());
  $: lastEmpty = !parts[parts.length - 1].text.trim();
  $: atThreadLimit = parts.length >= MAX_THREAD_REEDS;

  // Only called when the modal opens as a new reed.
  function loadDraft() {
    parts = draftTexts().map((text) => ({ key: nextKey++, text }));
  }

  function draftTexts() {
    const raw = localStorage.getItem('reedDraft');
    if (!raw) return [''];
    try {
      const texts = JSON.parse(raw);
      if (Array.isArray(texts) && texts.length > 0) return texts.map(String);
    } catch {}
    return [raw];
  }

  function handleContentChange() {
    if (saveDraftTimeout) clearTimeout(saveDraftTimeout);
    saveDraftTimeout = setTimeout(() => {
      if (threadable) localStorage.setItem('reedDraft', JSON.stringify(parts.map((p) => p.text)));
      draftSaved = true;
      setTimeout(() => { draftSaved = false; }, 2000);
    }, 1500);
  }

  async function focusPart(i) {
    await tick();
    textareas[i]?.focus();
  }

  function addPart(after = parts.length - 1, text = '') {
    if (parts.length >= MAX_THREAD_REEDS) return;
    parts = [...parts.slice(0, after + 1), { key: nextKey++, text }, ...parts.slice(after + 1)];
    handleContentChange();
    void focusPart(after + 1);
  }

  function removePart(i) {
    parts = parts.filter((_, j) => j !== i);
    handleContentChange();
    void focusPart(Math.max(0, i - 1));
  }

  // Longest prefix within the visible limit, backed off to a word break.
  function splitPoint(text) {
    let lo = 0;
    let hi = text.length;
    while (lo < hi) {
      const mid = Math.ceil((lo + hi) / 2);
      if (countMarkdownCharacters(text.slice(0, mid)) <= MAX_REED_VISIBLE_CHARS) lo = mid;
      else hi = mid - 1;
    }
    const space = text.lastIndexOf(' ', lo);
    return space > lo * 0.6 ? space : lo;
  }

  function splitOverflow(i) {
    const chunks = [];
    let rest = parts[i].text;
    const room = MAX_THREAD_REEDS - parts.length + 1;
    while (rest && chunks.length < room - 1 && countMarkdownCharacters(rest) > MAX_REED_VISIBLE_CHARS) {
      const cut = Math.max(1, splitPoint(rest));
      chunks.push(rest.slice(0, cut).trimEnd());
      rest = rest.slice(cut).trimStart();
    }
    if (rest) chunks.push(rest);
    const added = chunks.slice(1).map((text) => ({ key: nextKey++, text }));
    parts[i].text = chunks[0] ?? '';
    parts = [...parts.slice(0, i + 1), ...added, ...parts.slice(i + 1)];
    handleContentChange();
    void focusPart(i + added.length);
  }

  function onKeydown(e, i) {
    if (!threadable) return;
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && e.shiftKey) {
      e.preventDefault();
      if (parts[i].text.trim()) addPart(i);
    } else if (e.key === 'Backspace' && !parts[i].text && i > 0) {
      e.preventDefault();
      removePart(i);
    }
  }

  function clear() {
    parts = [{ key: nextKey++, text: '' }];
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

  function validate() {
    if (parts.length > MAX_THREAD_REEDS) return `A thread cannot have more than ${MAX_THREAD_REEDS} reeds.`;
    for (const [i, { text }] of parts.entries()) {
      const which = isThread ? ` (reed ${i + 1})` : '';
      if (contentRequired && !text.trim()) return `Cannot publish empty reed${which}.`;
      if (!reedContentWithinLimits(text)) {
        const visible = countMarkdownCharacters(text);
        return visible > MAX_REED_VISIBLE_CHARS
          ? `Message is too long${which} (${visible}/${MAX_REED_VISIBLE_CHARS} characters).`
          : `Message is too long${which} (${text.length}/${MAX_REED_RAW_CHARS} characters).`;
      }
    }
    return '';
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

      const activeKeyId = authService.getActiveKeyId();
      if (!activeKeyId) {
        errorMessage = 'No active key id found.';
        return;
      }

      if (hasPendingRevocation) {
        errorMessage = 'Your key is pending revocation. Publishing is disabled.';
        return;
      }

      errorMessage = validate();
      if (errorMessage) return;

      const keyId = authService.getActiveKeyId();
      const texts = parts.map((p) => p.text);
      /** @type {Reed | null} */
      let first = null;
      /** @type {Reed | null} */
      let prev = null;
      /** @type {Promise<boolean>[]} */
      const publishes = [];
      let chainPublished = true;

      for (const [i, text] of texts.entries()) {
        const reed = new Reed();
        reed.content = text;
        if (pinnedReply) {
          reed.replying = pinnedReply.id;
          reed.threadId = resolveThreadId(pinnedReply);
        } else if (first && prev) {
          // Each later part replies to the one before it, in the first part's thread.
          reed.replying = prev.id;
          reed.threadId = first.id;
        }
        if (pinnedEcho) {
          reed.echoing = pinnedEcho.id;
        }
        const detachedArmor = await requestSigner.sign(reed.asMarkdown());
        reed.setUserSignature(keyId, detachedArmor);
        const { publish } = await reedsService.createReed(reed);
        publishes.push(publish);
        // The server needs each parent countersigned before its reply.
        if (i < texts.length - 1 && chainPublished) chainPublished = await publish;
        first ??= reed;
        prev = reed;
      }

      const href = `/reed/${first?.id}`;
      // Keep the modal open (covering the feed/detail page underneath) until
      // the new route is ready, so a slow route load doesn't flash the page.
      await goto(href);
      close();
      Promise.all(publishes).then((results) => {
        if (results.every(Boolean)) return;
        const what = results.length > 1 ? 'thread' : 'reed';
        const message = get(isOnline)
          ? `There was an issue with the server. Your ${what} will be published automatically once it's resolved.`
          : `You're offline. We'll publish this ${what} as soon as you're back online.`;
        notificationStore.info(message, 10000);
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
      <ol class="parts" class:is-thread={isThread}>
        {#each parts as part, i (part.key)}
          <li class="part">
            {#if isThread}
              <div class="rail" aria-hidden="true">
                <span class="rail-dot">{i + 1}</span>
                {#if i < parts.length - 1}<span class="rail-line"></span>{/if}
              </div>
            {/if}
            <div class="part-body">
              <textarea
                bind:this={textareas[i]}
                placeholder={i === 0 ? placeholder : 'Keep going…'}
                aria-label={isThread ? `Reed ${i + 1} of ${parts.length}` : 'Reed'}
                rows={isThread ? 3 : 6}
                bind:value={part.text}
                on:keydown={(e) => onKeydown(e, i)}
                on:input={() => { handleContentChange(); mentionPickers[i]?.handleCaretChange(); }}
                on:click={() => mentionPickers[i]?.handleCaretChange()}
                on:keyup={(e) => {
                  if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) mentionPickers[i]?.handleCaretChange();
                }}
              ></textarea>
              <MentionPicker bind:this={mentionPickers[i]} textarea={textareas[i]} bind:content={part.text} bind:usernameHints={mentionUsernameHints} />
              <div class="content-info">
                {#if threadable && overVisible[i] && !atThreadLimit}
                  <button type="button" class="link-btn" on:click={() => splitOverflow(i)}>
                    Move overflow to a new reed
                  </button>
                {:else if isThread}
                  <span class="part-position">{i + 1}/{parts.length}</span>
                {:else}
                  <div class="draft-saved" class:hidden={!draftSaved}>Draft saved</div>
                {/if}
                <span class="part-tools">
                  <span class="character-counter" class:over-limit={overVisible[i]}>
                    {counts[i]}/{characterLimit}{isThread ? '' : ' characters'}
                  </span>
                  {#if isThread && i > 0}
                    <button type="button" class="remove-btn" on:click={() => removePart(i)} aria-label="Remove reed {i + 1}">✕</button>
                  {/if}
                </span>
              </div>
              {#if overRaw[i] && !overVisible[i]}
                <div class="error-message">
                  Message is too long ({part.text.length}/{rawCharacterLimit} characters).
                </div>
              {/if}
            </div>
          </li>
        {/each}
      </ol>
      {#if threadable}
        <button type="button" class="add-btn" on:click={() => addPart()} disabled={lastEmpty || atThreadLimit || isPublishing}>
          <span class="add-icon" aria-hidden="true">+</span>
          {atThreadLimit ? `Threads are limited to ${MAX_THREAD_REEDS} reeds` : isThread ? 'Extend thread' : 'Start thread'}
        </button>
        {#if isThread}
          <div class="draft-saved" class:hidden={!draftSaved}>Draft saved</div>
        {/if}
      {/if}
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
        <button type="submit" class="btn btn-primary" disabled={isPublishing || isOverLimit || hasPendingRevocation || (contentRequired && anyEmpty)}>
          {isPublishing ? 'Publishing...' : isThread ? 'Publish thread' : 'Publish'}
        </button>
      </div>
      <div class="reed-preview">
        <div class="reed-preview-label">Preview</div>
        {#each parts as part, i (part.key)}
          <div class="reed-preview-card" class:chained={isThread && i < parts.length - 1}>
            {#if isThread}<span class="preview-badge">{i + 1}/{parts.length}</span>{/if}
            <div class="reed-preview-body">
              {#if part.text.trim()}
                <MarkdownParser text={part.text} preview={true} usernameHints={mentionUsernameHints} />
              {:else}
                <p class="reed-preview-empty">{i === 0 ? 'Your reed will appear here as you type.' : 'Empty reed'}</p>
              {/if}
            </div>
          </div>
        {/each}
        <p class="reed-format-hint">
          Formatting: `code`, *bold*, _italic_, ~strike~, #hashtag, [label](url)
        </p>
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

  @media (min-width: 900px) {
    .write-modal {
      width: 380px;
      box-shadow: -12px 0 24px -16px rgba(0, 0, 0, 0.6);
    }
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

  .parts {
    list-style: none;
    margin: 1rem 0 0;
    padding: 0;
  }

  .part {
    display: flex;
    gap: 0.75rem;
  }

  .rail {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 1.75rem;
    flex-shrink: 0;
  }

  .rail-dot {
    width: 1.75rem;
    height: 1.75rem;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    font-size: 0.8rem;
    font-weight: 700;
    display: flex;
    align-items: center;
    justify-content: center;
    margin-top: 0.4rem;
  }

  .rail-line {
    flex: 1;
    width: 2px;
    margin: 0.25rem 0;
    background: var(--border);
  }

  .part-body {
    flex: 1;
    min-width: 0;
    padding-bottom: 0.75rem;
  }

  .part-body textarea {
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

  .is-thread .part-body textarea {
    min-height: 80px;
  }

  .content-info {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.8rem;
    color: var(--muted);
  }

  .part-position {
    margin-top: 0.5rem;
  }

  .part-tools {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .link-btn {
    margin-top: 0.5rem;
    background: none;
    border: none;
    padding: 0;
    color: var(--primary);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .remove-btn {
    margin-top: 0.5rem;
    background: none;
    border: none;
    color: var(--muted);
    cursor: pointer;
    padding: 0.1rem 0.3rem;
    border-radius: 4px;
  }

  .remove-btn:hover {
    color: var(--fg);
    background: var(--border);
  }

  .add-btn {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.6rem 0.75rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
    background: none;
    color: var(--primary);
    font-weight: 600;
    cursor: pointer;
  }

  .add-btn:hover:not(:disabled) {
    border-color: var(--primary);
  }

  .add-btn:disabled {
    color: var(--muted);
    cursor: not-allowed;
  }

  .add-icon {
    width: 1.25rem;
    height: 1.25rem;
    border-radius: 50%;
    border: 1.5px solid currentColor;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }

  .draft-saved {
    color: var(--primary);
    font-size: 0.8rem;
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
    position: relative;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
  }

  .reed-preview-card.chained {
    margin-bottom: 1rem;
  }

  .reed-preview-card.chained::after {
    content: '';
    position: absolute;
    left: 1.5rem;
    bottom: calc(-1rem - 1px);
    width: 2px;
    height: 1rem;
    background: var(--border);
  }

  .preview-badge {
    position: absolute;
    top: 0.5rem;
    right: 0.75rem;
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    color: var(--muted);
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

  .reed-format-hint {
    margin: 1rem 0 0;
    color: var(--muted);
    font-size: 0.8rem;
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
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    white-space: nowrap;
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
