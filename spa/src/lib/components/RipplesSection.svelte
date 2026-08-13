<script>
  // Wires the layout from specs/ripples/04_spa_ripples_section.md's mock to
  // real data: signed POST, verify-and-cache on fetch, soft-delete tombstones,
  // removed-account author rendering, and live delivery of new/updated
  // ripples via the reed's existing WS subscription (piggybacks on
  // ReedStatsSubscription, mounted by the parent page — no separate
  // subscribe call needed here, same as ConversationSection's REED_REPLIES).
  import { onDestroy, onMount } from 'svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import RippleComposer from '$lib/components/RippleComposer.svelte';
  import { apiService } from '$lib/services/api';
  import { userRepository } from '$lib/repositories/user';
  import { ripplesRepository } from '$lib/repositories/ripples';
  import { formatRelativeTime } from '$lib/utils/time';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';

  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let reedID;

  /** Bound out to the parent for tab-count display. */
  export let count = 0;

  const SEC = 1000;
  const MIN = 60 * SEC;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;

  /** @type {import('$lib/types/api').Ripple[]} */
  let ripples = [];
  /** username resolved for each ripple's userID, or null if unresolvable
   * (removed account) — keyed by userID, see specs/ripples/04's
   * "Rendering a removed-account commenter" section. */
  let usernames = {};

  let loading = true;
  /** Local deadline in performance.now() units, derived once per fetch
   * from the server's relative expiresInSeconds — never from an absolute
   * timestamp compared against this device's wall clock, which could be
   * skewed. performance.now() is monotonic (immune to system clock
   * adjustments mid-session), unlike Date.now(). */
  let expiresAtMonotonic = /** @type {number | null} */ (null);
  let nextCursor = /** @type {string | undefined} */ (undefined);
  let hasMore = false;
  let loadingMore = false;
  /** True once the local countdown reaches zero. Drives the fire
   * animation and then clears the section — belt-and-suspenders against
   * the section rendering stale content the server hasn't swept yet
   * (a fetch that lands in the race window right before a cron tick, or
   * a WS event delivered after this client already considers the thread
   * gone). */
  let expired = false;
  /** True for the duration of the fire animation, then false — separate
   * from `expired` so the burning ripples stay visible (and burning)
   * during the animation instead of vanishing the instant the countdown
   * hits zero. */
  let burning = false;

  $: count = expired ? 0 : ripples.length;

  let nowTick = performance.now();
  const tickTimer = setInterval(() => { nowTick = performance.now(); }, 1000);
  onDestroy(() => clearInterval(tickTimer));

  // Single-row burn animation is 800ms; each row's start is staggered by
  // BURN_STAGGER_MS, capped at MAX_BURN_STAGGER_MS so a long list's last
  // rows can't get their animation truncated by the section clearing —
  // total time is always the single-row duration plus the capped stagger.
  const BURN_ROW_MS = 800;
  const BURN_STAGGER_MS = 60;
  const MAX_BURN_STAGGER_MS = 600;
  const BURN_DURATION_MS = BURN_ROW_MS + MAX_BURN_STAGGER_MS;
  let burnTimer = /** @type {ReturnType<typeof setTimeout> | null} */ (null);
  onDestroy(() => { if (burnTimer) clearTimeout(burnTimer); });

  $: if (!expired && expiresAtMonotonic != null && nowTick >= expiresAtMonotonic) {
    triggerExpiry();
  }

  function triggerExpiry() {
    if (expired || burning) return;
    burning = true;
    burnTimer = setTimeout(() => {
      burning = false;
      expired = true;
      ripples = [];
    }, BURN_DURATION_MS);
  }

  function formatCountdown(targetMonotonic, fromMonotonic) {
    const remaining = targetMonotonic - fromMonotonic;
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

    // Defensive: if the server itself reports the section as already
    // expired (a fetch landing in the race window right before the next
    // cron sweep), don't render whatever it sent — treat it the same as
    // a client-side countdown hitting zero, just without the animation
    // (nothing was visibly alive to burn).
    if (res.expiresInSeconds != null && res.expiresInSeconds <= 0) {
      ripples = [];
      expired = true;
      return;
    }

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
    if (res.expiresInSeconds != null) {
      expiresAtMonotonic = performance.now() + res.expiresInSeconds * 1000;
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

  /** Insert a ripple at the position that matches server ordering, not at
   * the end of the array: right after its replyingTo target if one is
   * loaded (a reply belongs immediately after the message it replies to,
   * within that thread's run), otherwise at the very end (a new top-level
   * thread). This is what keeps a freshly-posted or live-delivered reply
   * from jumping across the whole list on the next reload — see
   * specs/ripples/04_spa_ripples_section.md's Data flow section. A
   * genuinely concurrent reply to the same target can still land in a
   * slightly different spot than the server's final thread-grouped order;
   * accepted tradeoff, not worth a full resort against a partial page. */
  function insertRipple(ripple) {
    // The section already burned away locally — don't resurrect it for a
    // straggling WS event or a just-completed post whose response arrived
    // after the countdown hit zero.
    if (expired) return;
    if (ripples.some((r) => r.hash === ripple.hash)) return;
    const targetIndex = ripple.replyingTo
      ? ripples.findIndex((r) => r.hash === ripple.replyingTo)
      : -1;
    if (targetIndex === -1) {
      ripples = [...ripples, ripple];
    } else {
      ripples = [...ripples.slice(0, targetIndex + 1), ripple, ...ripples.slice(targetIndex + 1)];
    }
  }

  /** RIPPLE_POSTED: verify-or-discard (same path a list fetch uses), then
   * insert if not already present — guards against the optimistic-insert-
   * then-echo double-add for the poster's own just-submitted ripple. */
  async function handleRipplePosted(msg) {
    if (expired || msg?.userID !== userID || msg?.reedID !== reedID || !msg?.ripple) return;
    const ripple = msg.ripple;
    if (ripples.some((r) => r.hash === ripple.hash)) return;
    const ok = await ripplesRepository.storeRipple(ripple, userID, reedID);
    if (!ok) return;
    await resolveUsername(ripple.userID);
    insertRipple(ripple);
  }

  /** RIPPLE_UPDATED: patch the matching row in place (deleted/content) —
   * never remove it. Does not re-verify (verifyRipple's tombstone
   * short-circuit already trusts the deleted flag). */
  async function handleRippleUpdated(msg) {
    if (expired || msg?.userID !== userID || msg?.reedID !== reedID || !msg?.ripple) return;
    const ripple = msg.ripple;
    await ripplesRepository.storeRipple(ripple, userID, reedID);
    ripples = ripples.map((r) => (r.hash === ripple.hash ? ripple : r));
  }

  onMount(() => {
    serverConnection.on(ServerEvent.RipplePosted, handleRipplePosted);
    serverConnection.on(ServerEvent.RippleUpdated, handleRippleUpdated);
  });

  onDestroy(() => {
    serverConnection.off(ServerEvent.RipplePosted, handleRipplePosted);
    serverConnection.off(ServerEvent.RippleUpdated, handleRippleUpdated);
  });

  /** The ripple being replied to, or null while the composer sits at the
   * bottom for a top-level post. Set by clicking "reply" on a row; moves
   * the composer to render right after that row instead of at the list's
   * end, so the reply target stays visible while typing — see the
   * component's own header comment for why this replaced the old
   * always-at-the-bottom layout. */
  let replyingTo = /** @type {import('$lib/types/api').Ripple | null} */ (null);

  function startReply(ripple) {
    replyingTo = ripple;
  }

  function cancelReply() {
    replyingTo = null;
  }

  /** RippleComposer's `posted` event: insert at the same position the
   * composer itself was rendered at (right after replyingTo, or at the
   * end for top-level) — matches server ordering in the common case, so
   * nothing jumps position on the next reload. */
  async function handleComposerPosted(event) {
    const { ripple: posted } = event.detail;
    const ok = await ripplesRepository.storeRipple(posted, userID, reedID);
    if (ok) {
      await resolveUsername(posted.userID);
      insertRipple(posted);
    }
    replyingTo = null;
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
  {#if !loading && !expired && ripples.length > 0 && expiresAtMonotonic != null}
    <div class="ripples-header">
      <span class="ripples-countdown" class:ripples-countdown--burning={burning} title="Whole thread disappears if nobody replies before this">
        <span class="countdown-dot"></span>
        {burning ? 'gone' : `gone in ${formatCountdown(expiresAtMonotonic, nowTick)}`}
      </span>
    </div>
  {/if}

  {#if loading}
    <p class="ripples-empty">Loading…</p>
  {:else if expired}
    <p class="ripples-empty ripples-empty--expired">
      <span class="ripples-empty-icon" aria-hidden="true">🔥</span>
      This thread burned away — nobody kept it alive in time.
    </p>
  {:else if ripples.length === 0}
    <p class="ripples-empty">No ripples yet — be the first to say something.</p>
  {:else}
    <ul class="ripple-list" class:ripple-list--burning={burning}>
      {#each ripples as ripple, i (ripple.hash)}
        <li
          class="ripple-row"
          class:ripple-row--reply={!!ripple.replyingTo}
          style={burning ? `--burn-delay: ${Math.min(i * BURN_STAGGER_MS, MAX_BURN_STAGGER_MS)}ms` : undefined}
        >
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
              <span class="ripple-meta-text">
                {#if usernames[ripple.userID]}
                  <Username userID={ripple.userID} username={usernames[ripple.userID]} color="var(--muted)" />
                {:else}
                  <span class="ripple-username-removed">[removed account]</span>
                {/if}
                · {formatRelativeTime(ripple.postedAt)}
              </span>
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
        {#if replyingTo?.hash === ripple.hash}
          <li class="ripple-composer-row">
            <RippleComposer
              {userID}
              {reedID}
              {replyingTo}
              replyingToUsername={usernames[replyingTo.userID] ?? null}
              autofocus
              on:posted={handleComposerPosted}
              on:cancel={cancelReply}
            />
          </li>
        {/if}
      {/each}
    </ul>
    {#if hasMore}
      <button type="button" class="ripple-action load-more-btn" on:click={loadMore} disabled={loadingMore}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </button>
    {/if}
  {/if}

  {#if !expired && !replyingTo}
    <RippleComposer
      {userID}
      {reedID}
      on:posted={handleComposerPosted}
    />
  {/if}

  {#if !expired}
    <p class="ripples-why-explainer">
      Ripples aren't saved permanently — this thread disappears 7 days after
      its last reply, and posting a new one resets the countdown. Plain text
      only — markdown isn't supported.
    </p>
  {/if}
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

  .ripples-countdown--burning {
    color: #ff6a1a;
  }

  .ripples-countdown--burning .countdown-dot {
    background: #ff6a1a;
    animation: ember-pulse 0.5s ease-in-out infinite alternate;
  }

  .ripples-empty--expired {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-style: normal;
  }

  .ripples-empty-icon {
    font-size: 1.1rem;
    line-height: 1;
    animation: ember-pulse 1.2s ease-in-out infinite alternate;
  }

  /* Each row fades/rises away in its own delayed pass (--burn-delay,
     set inline per-row) so the whole list doesn't vanish as one flat
     block — closer to embers catching one after another. */
  .ripple-list--burning .ripple-row {
    animation: ripple-burn 0.8s ease-in forwards;
    animation-delay: var(--burn-delay, 0ms);
  }

  @keyframes ripple-burn {
    0% {
      opacity: 1;
      filter: none;
      transform: translateY(0) scale(1);
    }
    30% {
      opacity: 1;
      filter: brightness(1.6) saturate(1.8) hue-rotate(-25deg);
      transform: translateY(-2px) scale(1.01);
    }
    100% {
      opacity: 0;
      filter: brightness(1.6) saturate(1.8) hue-rotate(-25deg) blur(2px);
      transform: translateY(-14px) scale(0.97);
    }
  }

  @keyframes ember-pulse {
    from {
      opacity: 0.6;
      transform: scale(0.9);
    }
    to {
      opacity: 1;
      transform: scale(1.15);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .ripple-list--burning .ripple-row {
      animation: ripple-burn-reduced 0.3s ease-in forwards;
      animation-delay: 0ms;
    }
    .ripples-countdown--burning .countdown-dot,
    .ripples-empty-icon {
      animation: none;
    }
  }

  @keyframes ripple-burn-reduced {
    from { opacity: 1; }
    to { opacity: 0; }
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

  /* Not a full nested-reply indent (00's lock: flat rendering) — just a
     visual hint that this response is part of a thread, not top-level. */
  .ripple-row--reply {
    margin-left: 1rem;
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
    align-items: baseline;
    margin: 0;
    font-size: 0.82rem;
  }

  .ripple-meta-text {
    color: var(--muted);
    min-width: 0;
  }

  .ripple-username-removed {
    font-style: italic;
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
    margin: 0 0 0.3rem;
    font-size: 0.88rem;
    line-height: 1.45;
    white-space: pre-wrap;
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

  /* No list-item box model needed — this <li> only exists so the inline
     composer can sit between two <li> siblings in the same <ul>. */
  .ripple-composer-row {
    list-style: none;
    margin: 0.4rem 0;
  }
</style>
