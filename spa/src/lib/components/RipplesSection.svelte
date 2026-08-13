<script>
  // Wires the layout from specs/ripples/04_spa_ripples_section.md's mock to
  // real data: signed POST, verify-and-cache on fetch, soft-delete tombstones,
  // removed-account author rendering. No WS live delivery yet (03 is
  // unimplemented server-side) — this degrades gracefully to fetch-on-mount +
  // optimistic local append, per specs/ripples/README.md's Parallelism note.
  import { onDestroy, onMount } from 'svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { userRepository } from '$lib/repositories/user';
  import { ripplesRepository } from '$lib/repositories/ripples';
  import { cryptoService } from '$lib/services/crypto';
  import { buildRippleUserPayload } from '$lib/services/signing';
  import { formatRelativeTime } from '$lib/utils/time';

  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let reedID;

  /** Bound out to the parent for tab-count display. */
  export let count = 0;

  const MAX_RIPPLE_CHARS = 140; // MAX_REED_VISIBLE_CHARS, per spec 00/04

  const MIN = 60_000;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;

  /** @type {import('$lib/types/api').Ripple[]} */
  let ripples = [];
  /** username resolved for each ripple's userID, or null if unresolvable
   * (removed account) — keyed by userID, see specs/ripples/04's
   * "Rendering a removed-account commenter" section. */
  let usernames = {};

  let loading = true;
  let expiresAt = /** @type {number | null} */ (null);
  let nextCursor = /** @type {string | undefined} */ (undefined);
  let hasMore = false;
  let loadingMore = false;

  $: count = ripples.length;

  let nowTick = Date.now();
  const tickTimer = setInterval(() => { nowTick = Date.now(); }, 1000);
  onDestroy(() => clearInterval(tickTimer));

  function formatCountdown(targetMs, fromMs) {
    const remaining = targetMs - fromMs;
    if (remaining <= 0) return 'expiring…';
    const d = Math.floor(remaining / DAY);
    const h = Math.floor((remaining % DAY) / HOUR);
    const m = Math.floor((remaining % HOUR) / MIN);
    const s = Math.floor((remaining % MIN) / 1000);
    if (d > 0) return `${d}d ${h}h`;
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  }

  async function resolveUsername(uid) {
    if (uid in usernames) return usernames[uid];
    const user = await userRepository.getByUserId(uid).catch(() => null);
    usernames = { ...usernames, [uid]: user?.username ?? null };
    return usernames[uid];
  }

  function findByHash(hash) {
    return ripples.find((r) => r.hash === hash) ?? null;
  }

  async function loadPage(before) {
    const res = await apiService.listRipples(userID, reedID, { limit: 50, before });
    const kept = [];
    for (const ripple of res.responses) {
      const ok = await ripplesRepository.storeRipple(ripple, userID, reedID);
      if (ok) kept.push(ripple);
    }
    for (const ripple of kept) {
      await resolveUsername(ripple.userID);
    }
    ripples = before ? [...ripples, ...kept] : kept;
    hasMore = res.hasMore;
    nextCursor = res.nextCursor;
    if (res.expiresAt) {
      expiresAt = Date.parse(res.expiresAt);
    }
  }

  async function loadMore() {
    if (!hasMore || loadingMore || !nextCursor) return;
    loadingMore = true;
    try {
      await loadPage(nextCursor);
    } catch (error) {
      console.error('Failed to load more ripples:', error);
    } finally {
      loadingMore = false;
    }
  }

  onMount(async () => {
    try {
      await loadPage(undefined);
    } catch (error) {
      console.error('Failed to load ripples:', error);
    } finally {
      loading = false;
    }
  });

  let draft = '';
  let replyingTo = /** @type {import('$lib/types/api').Ripple | null} */ (null);
  let posting = false;
  let postError = '';
  $: remaining = MAX_RIPPLE_CHARS - draft.length;
  $: overLimit = remaining < 0;

  function startReply(ripple) {
    replyingTo = ripple;
  }

  function cancelReply() {
    replyingTo = null;
  }

  async function submitRipple() {
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
        userID,
        reedID,
        user.id,
        fingerprint,
        threadID,
        replyingToHash ?? '',
        content
      );
      const detachedArmor = await cryptoService.signMessage(userPayload, keyData.armor, passphrase);
      const userSignature = btoa(detachedArmor.trim()).trim();

      const posted = await apiService.postRipple(userID, reedID, {
        content,
        threadID,
        replyingTo: replyingToHash,
        fingerprint,
        userSignature,
      });

      const ok = await ripplesRepository.storeRipple(posted, userID, reedID);
      if (ok) {
        await resolveUsername(posted.userID);
        ripples = [...ripples, posted];
      }

      draft = '';
      replyingTo = null;
    } catch (error) {
      console.error('Failed to post ripple:', error);
      postError = error?.message || 'Failed to post comment';
    } finally {
      posting = false;
    }
  }

  async function deleteRipple(hash) {
    if (!confirm('Are you sure you want to delete this ripple?')) {
      return;
    }
    try {
      await apiService.deleteRipple(hash);
      ripples = ripples.map((r) =>
        r.hash === hash ? { ...r, deleted: true, content: '[DELETED]' } : r
      );
    } catch (error) {
      console.error('Failed to delete ripple:', error);
    }
  }

  let ownUserID = '';
  onMount(() => {
    ownUserID = localStorage.getItem('userId') ?? '';
  });
</script>

<section class="ripples-section" aria-label="Ripples">
  {#if !loading && ripples.length > 0 && expiresAt != null}
    <div class="ripples-header">
      <span class="ripples-countdown" title="Whole thread disappears if nobody replies before this">
        <span class="countdown-dot"></span>
        gone in {formatCountdown(expiresAt, nowTick)}
      </span>
    </div>
  {/if}

  {#if loading}
    <p class="ripples-empty">Loading…</p>
  {:else if ripples.length === 0}
    <p class="ripples-empty">No ripples yet — be the first to say something.</p>
  {:else}
    <ul class="ripple-list">
      {#each ripples as ripple (ripple.hash)}
        <li class="ripple-row">
          <div class="ripple-avatar">
            <Avatar userID={ripple.userID} username={usernames[ripple.userID] ?? ''} size="32px" />
          </div>
          <div class="ripple-body">
            <p class="ripple-meta">
              {#if ripple.userID === ownUserID && !ripple.deleted}
                <button type="button" class="ripple-delete-btn" on:click={() => deleteRipple(ripple.hash)} aria-label="Delete ripple">
                  <span class="ripple-delete-icon"></span>
                </button>
              {/if}
              {#if usernames[ripple.userID]}
                <span class="ripple-username"><Username userID={ripple.userID} username={usernames[ripple.userID]} /></span>
              {:else}
                <span class="ripple-username ripple-username-removed">[removed account]</span>
              {/if}
              <span class="ripple-time">{formatRelativeTime(ripple.postedAt)}</span>
            </p>
            {#if ripple.replyingTo}
              <p class="ripple-reply-chip">
                replying to {#if findByHash(ripple.replyingTo)}
                  {#if usernames[findByHash(ripple.replyingTo).userID]}
                    @{usernames[findByHash(ripple.replyingTo).userID]}
                  {:else}
                    a removed account
                  {/if}
                {:else}
                  a comment
                {/if}
              </p>
            {/if}
            {#if ripple.deleted}
              <p class="ripple-content ripple-content-deleted">[DELETED]</p>
            {:else}
              <p class="ripple-content">
                {ripple.content}
                <button type="button" class="ripple-action ripple-reply-inline" on:click={() => startReply(ripple)}>reply</button>
              </p>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
    {#if hasMore}
      <button type="button" class="ripple-action load-more-btn" on:click={loadMore} disabled={loadingMore}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </button>
    {/if}
  {/if}

  <div class="ripple-composer">
    {#if replyingTo}
      <div class="reply-chip-bar">
        <span>Replying to {usernames[replyingTo.userID] ? `@${usernames[replyingTo.userID]}` : 'a removed account'}</span>
        <button type="button" class="chip-dismiss" on:click={cancelReply} aria-label="Cancel reply">×</button>
      </div>
    {/if}
    <textarea
      class="ripple-textarea"
      placeholder="Join the ripple…"
      bind:value={draft}
      maxlength={MAX_RIPPLE_CHARS + 20}
      rows="2"
      disabled={posting}
    ></textarea>
    {#if postError}
      <p class="ripple-post-error">{postError}</p>
    {/if}
    <div class="composer-footer">
      <span class="char-counter" class:over={overLimit}>{remaining}</span>
      <button type="button" class="post-btn" disabled={!draft.trim() || overLimit || posting} on:click={submitRipple}>
        {posting ? 'Posting…' : 'Post'}
      </button>
    </div>
  </div>

  <p class="ripples-why-explainer">
    Ripples aren't saved permanently — this thread disappears 7 days after
    its last reply, and posting a new one resets the countdown.
  </p>
</section>

<style>
  .ripples-section {
    padding-top: 1rem;
  }

  .ripples-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0 0.75rem 0.75rem;
  }

  .ripples-countdown {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.75rem;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .countdown-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #e0a030;
    flex-shrink: 0;
  }

  .ripples-why-explainer {
    margin: 0 0.75rem;
    padding: 0.6rem 0.7rem;
    background: var(--input-bg, rgba(127, 127, 127, 0.08));
    border-radius: 8px;
    font-size: 0.8rem;
    line-height: 1.5;
    color: var(--muted);
  }

  .ripples-empty {
    margin: 0 0.75rem 1rem;
    color: var(--muted);
    font-size: 0.9rem;
    font-style: italic;
  }

  .ripple-list {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0 0.75rem;
  }

  .ripple-row {
    display: flex;
    gap: 0.5rem;
    padding: 0.25rem 0;
  }

  .ripple-avatar {
    flex: 0 0 auto;
    padding-top: 0.1rem;
  }

  .ripple-body {
    min-width: 0;
    flex: 1 1 auto;
  }

  .ripple-meta {
    display: flex;
    align-items: center;
    margin: 0 0 0.15rem;
    font-size: 0.82rem;
  }

  .ripple-username {
    font-weight: 600;
    color: var(--fg);
  }

  .ripple-username-removed {
    font-weight: 400;
    font-style: italic;
    color: var(--muted);
  }

  .ripple-time {
    color: var(--muted);
    margin-left: 0.4rem;
  }

  .ripple-delete-btn {
    display: inline-flex;
    flex: 0 0 auto;
    width: auto;
    align-items: center;
    background: none;
    border: none;
    padding: 0;
    margin: 0 0.4rem 0 0;
    line-height: 0;
    cursor: pointer;
    color: var(--muted);
    opacity: 0.7;
  }

  .ripple-delete-btn:hover {
    opacity: 1;
    color: #d9534f;
  }

  .ripple-delete-icon {
    display: inline-block;
    width: 0.85rem;
    height: 0.85rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/trash-16.png');
    mask-image: url('/icons/trash-16.png');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .ripple-reply-chip {
    margin: 0 0 0.25rem;
    font-size: 0.78rem;
    color: var(--muted);
    font-style: italic;
  }

  .ripple-content {
    margin: 0 0 0.3rem;
    font-size: 0.88rem;
    line-height: 1.45;
    color: var(--fg);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .ripple-content-deleted {
    color: var(--muted);
    font-style: italic;
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

  .ripple-reply-inline {
    margin-left: 0.5rem;
    white-space: nowrap;
  }

  .load-more-btn {
    margin: 0 0.75rem 1rem;
    font-size: 0.8rem;
  }

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
