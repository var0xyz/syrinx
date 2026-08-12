<script>
  // UI-only PoC of specs/ripples/00_design.md + 04_spa_ripples_section.md.
  // Everything below is hardcoded — no api.ts calls, no WS, no persistence.
  // Purely here to look at layout/copy/interaction shape before real work starts.
  // These aren't reeds — no card, just identicon + name + text like an HN
  // comment thread (flat, dense, plain text).
  import { onDestroy } from 'svelte';
  import Avatar from '$lib/components/Avatar.svelte';

  /** Bound out to the parent for tab-count display. */
  export let count = 0;

  const MAX_RIPPLE_CHARS = 140; // MAX_REED_VISIBLE_CHARS, per spec 00/04

  const now = Date.now();
  const MIN = 60_000;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;

  /** @typedef {{ id: string; userID: string; username: string; content: string; postedAt: number; inReplyToRippleID?: string; mine?: boolean }} Ripple */

  /** @type {Ripple[]} */
  let ripples = [
    {
      id: 'r1',
      userID: 'u-astra',
      username: 'astra',
      content: 'Wait, does this mean the coverage percent recomputes every time a holder drops offline?',
      postedAt: now - 6 * DAY - 20 * HOUR,
    },
    {
      id: 'r2',
      userID: 'u-me',
      username: 'you',
      content: 'Yeah — it\'s a live snapshot, not cached. Same query the stats subscription already runs.',
      postedAt: now - 6 * DAY - 19 * HOUR,
      inReplyToRippleID: 'r1',
      mine: true,
    },
    {
      id: 'r3',
      userID: 'u-fennel',
      username: 'fennel',
      content: 'kind of wild that this whole thread just... vanishes in a week if nobody says anything else',
      postedAt: now - 2 * HOUR,
    },
    {
      id: 'r4',
      userID: 'u-astra',
      username: 'astra',
      content: '@fennel that\'s the point though — no archival guilt, no dead comment sections nobody reads',
      postedAt: now - 40 * MIN,
      inReplyToRippleID: 'r3',
    },
    {
      id: 'r5',
      userID: 'u-deleted-thread-demo',
      username: 'wisp',
      content: 'replying to something that\'s already gone, to show the fallback line',
      postedAt: now - 12 * MIN,
      inReplyToRippleID: 'r-expired-and-gone',
    },
  ];

  $: count = ripples.length;

  // Thread's last-activity clock — bumped by the newest ripple's postedAt.
  $: lastActivityAt = ripples.length
    ? Math.max(...ripples.map((r) => r.postedAt))
    : now;
  $: expiresAt = lastActivityAt + 7 * DAY;

  let nowTick = now;
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

  function formatRelative(ts, fromMs) {
    const diffSeconds = Math.floor((fromMs - ts) / 1000);
    if (diffSeconds < 15) return 'just now';
    if (diffSeconds < 60) return `${diffSeconds}s ago`;
    const diffMinutes = Math.floor(diffSeconds / 60);
    if (diffMinutes < 60) return `${diffMinutes}m ago`;
    const diffHours = Math.floor(diffMinutes / 60);
    if (diffHours < 24) return `${diffHours}h ago`;
    const diffDays = Math.floor(diffHours / 24);
    return `${diffDays}d ago`;
  }

  function usernameFor(rippleID) {
    return ripples.find((r) => r.id === rippleID)?.username ?? null;
  }

  let draft = '';
  let replyingTo = /** @type {Ripple | null} */ (null);
  $: remaining = MAX_RIPPLE_CHARS - draft.length;
  $: overLimit = remaining < 0;

  function startReply(ripple) {
    replyingTo = ripple;
  }

  function cancelReply() {
    replyingTo = null;
  }

  function submitRipple() {
    const content = draft.trim();
    if (!content || overLimit) return;
    ripples = [
      ...ripples,
      {
        id: `local-${Date.now()}`,
        userID: 'u-me',
        username: 'you',
        content,
        postedAt: Date.now(),
        inReplyToRippleID: replyingTo?.id,
        mine: true,
      },
    ];
    draft = '';
    replyingTo = null;
  }

  function deleteRipple(id) {
    if (!confirm('Are you sure you want to delete this ripple?')) {
      return;
    }
    ripples = ripples.filter((r) => r.id !== id);
  }
</script>

<section class="ripples-section" aria-label="Ripples">
  {#if ripples.length > 0}
    <div class="ripples-header">
      <span class="ripples-countdown" title="Whole thread disappears if nobody replies before this">
        <span class="countdown-dot"></span>
        gone in {formatCountdown(expiresAt, nowTick)}
      </span>
    </div>
  {/if}

  {#if ripples.length === 0}
    <p class="ripples-empty">No ripples yet — be the first to say something.</p>
  {:else}
    <ul class="ripple-list">
      {#each ripples as ripple (ripple.id)}
        <li class="ripple-row">
          <div class="ripple-avatar">
            <Avatar userID={ripple.userID} username={ripple.username} size="32px" />
          </div>
          <div class="ripple-body">
            <p class="ripple-meta">
              {#if ripple.mine}
                <button type="button" class="ripple-delete-btn" on:click={() => deleteRipple(ripple.id)} aria-label="Delete ripple">
                  <span class="ripple-delete-icon"></span>
                </button>
              {/if}
              <span class="ripple-username">{ripple.username}</span>
              <span class="ripple-time">{formatRelative(ripple.postedAt, nowTick)}</span>
            </p>
            {#if ripple.inReplyToRippleID}
              <p class="ripple-reply-chip">
                replying to {#if usernameFor(ripple.inReplyToRippleID)}@{usernameFor(ripple.inReplyToRippleID)}{:else}a deleted comment{/if}
              </p>
            {/if}
            <p class="ripple-content">
              {ripple.content}
              <button type="button" class="ripple-action ripple-reply-inline" on:click={() => startReply(ripple)}>reply</button>
            </p>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <div class="ripple-composer">
    {#if replyingTo}
      <div class="reply-chip-bar">
        <span>Replying to @{replyingTo.username}</span>
        <button type="button" class="chip-dismiss" on:click={cancelReply} aria-label="Cancel reply">×</button>
      </div>
    {/if}
    <textarea
      class="ripple-textarea"
      placeholder="Join the ripple…"
      bind:value={draft}
      maxlength={MAX_RIPPLE_CHARS + 20}
      rows="2"
    ></textarea>
    <div class="composer-footer">
      <span class="char-counter" class:over={overLimit}>{remaining}</span>
      <button type="button" class="post-btn" disabled={!draft.trim() || overLimit} on:click={submitRipple}>
        Post
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
