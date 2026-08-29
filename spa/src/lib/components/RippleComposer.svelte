<script>
  // The ripple composer, rendered by the parent RipplesSection either as
  // the top-level composer (behind the "post ripple" button, no
  // replyingTo) or inline right after any ripple being replied to — several
  // reply instances can be mounted at once, each independent. Owns its own
  // draft/signing/post state; tells the parent what happened via events
  // rather than reaching back into the parent's ripple list itself — where
  // the newly-posted ripple gets inserted is the parent's call.
  import { createEventDispatcher, onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { cryptoService } from '$lib/services/crypto';
  import { buildRippleUserPayload } from '$lib/services/signing';
  import { apiService } from '$lib/services/api';

  /** The parent reed's canonical id (authorID@serverID/uuid). */
  /** @type {string} */
  export let reedID;
  /** The parent reed's base64 server-signature armor — proof of
   * possession required to post a ripple (see api.ts's postRipple). */
  /** @type {string} */
  export let serverSignatureArmor;
  /** The ripple being replied to, or null for a top-level post. */
  export let replyingTo = /** @type {import('$lib/types/api').Ripple | null} */ (null);
  /** Resolved username for replyingTo.userID, or null (removed account) —
   * parent already has this cached, no need to refetch here. */
  export let replyingToUsername = /** @type {string | null} */ (null);
  export let autofocus = false;

  const dispatch = createEventDispatcher();

  const MAX_RIPPLE_CHARS = 140; // MAX_REED_VISIBLE_CHARS, per spec 00/04 — ripples are plain text, not markdown

  let draft = '';
  let posting = false;
  let postError = '';
  /** @type {HTMLTextAreaElement | undefined} */
  let textareaEl;
  $: remaining = MAX_RIPPLE_CHARS - draft.length;
  $: overLimit = remaining < 0;

  onMount(() => {
    if (autofocus) textareaEl?.focus();
  });

  function cancel() {
    dispatch('cancel');
  }

  async function submit() {
    if (posting) return;
    const content = draft.trim();
    if (!content || overLimit) return;

    postError = '';
    posting = true;
    try {
      const user = await authService.getCurrentUser();
      if (!user) throw new Error('No user ID found. Please log in.');

      const fingerprint = authService.getActiveKeyFingerprint();
      if (!fingerprint) throw new Error('No active key fingerprint found.');

      const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
      if (!keyData) throw new Error('Private key not found. Please import your key.');

      const passphrase = authService.getPassphrase();
      if (!passphrase) throw new Error('Session expired. Please sign in again.');

      const threadID = replyingTo ? replyingTo.threadID : crypto.randomUUID();
      const replyingToHash = replyingTo?.hash;

      const userPayload = buildRippleUserPayload(
        reedID,
        user.id,
        fingerprint,
        threadID,
        replyingToHash ?? '',
        content
      );
      const detachedArmor = await cryptoService.signMessage(userPayload, keyData.armor, passphrase);
      const userSignature = btoa(detachedArmor.trim()).trim();

      const posted = await apiService.postRipple(reedID, {
        content,
        threadID,
        replyingTo: replyingToHash,
        proof: serverSignatureArmor,
        keyID: fingerprint,
        userSignature,
      });

      draft = '';
      dispatch('posted', { ripple: posted, replyingTo });
    } catch (error) {
      console.error('Failed to post ripple:', error);
      postError = error?.message || 'Failed to post comment';
    } finally {
      posting = false;
    }
  }
</script>

<div class="ripple-composer">
  {#if replyingTo}
    <div class="reply-chip-bar">
      <span>Replying to {replyingToUsername ? `@${replyingToUsername}` : 'a removed account'}</span>
      <button type="button" class="chip-dismiss" on:click={cancel} aria-label="Cancel reply">×</button>
    </div>
  {/if}
  <textarea
    class="ripple-textarea"
    placeholder="Join the ripple…"
    bind:value={draft}
    bind:this={textareaEl}
    maxlength={MAX_RIPPLE_CHARS + 20}
    rows="2"
    disabled={posting}
  ></textarea>
  {#if postError}
    <p class="ripple-post-error">{postError}</p>
  {/if}
  <div class="composer-footer">
    <span class="char-counter" class:over={overLimit}>{remaining}</span>
    <div class="composer-footer-actions">
      {#if !replyingTo}
        <button type="button" class="ripple-action" on:click={cancel}>Cancel</button>
      {/if}
      <button type="button" class="post-btn" disabled={!draft.trim() || overLimit || posting} on:click={submit}>
        {posting ? 'Posting…' : 'Post'}
      </button>
    </div>
  </div>
</div>

<style>
  .ripple-composer {
    margin: 0 0.75rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.6rem;
    background: var(--bg);
  }

  .reply-chip-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--input-bg, rgba(127,127,127,0.1));
    border-radius: 6px;
    padding: 0.25rem 0.5rem;
    margin-bottom: 0.5rem;
    font-size: 0.78rem;
    color: var(--muted);
  }

  .chip-dismiss {
    width: auto;
    background: none;
    border: none;
    color: var(--muted);
    font-size: 1rem;
    line-height: 1;
    cursor: pointer;
    padding: 0 0.25rem;
  }

  .ripple-textarea {
    width: 100%;
    resize: vertical;
    min-height: 3rem;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    padding: 0;
  }

  .ripple-textarea:focus {
    outline: none;
  }

  .ripple-post-error {
    margin: 0.4rem 0 0;
    font-size: 0.78rem;
    color: #d9534f;
  }

  .composer-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 0.4rem;
  }

  .composer-footer-actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .ripple-action {
    display: inline;
    width: auto;
    background: none;
    border: none;
    padding: 0;
    margin: 0;
    font-size: 0.75rem;
    color: var(--muted);
    text-align: left;
    cursor: pointer;
  }

  .ripple-action:hover {
    color: var(--fg);
    text-decoration: underline;
  }

  .char-counter {
    font-size: 0.75rem;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .char-counter.over {
    color: #d9534f;
    font-weight: 600;
  }

  .post-btn {
    width: auto;
    background: var(--primary);
    color: white;
    border: none;
    border-radius: 999px;
    padding: 0.35rem 1rem;
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
  }

  .post-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
