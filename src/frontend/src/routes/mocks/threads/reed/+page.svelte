<script>
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { formatAbsoluteDateTime, formatRelativeTime } from '$lib/utils/time';
  import { author, thread, replies } from '../data.js';

  const total = thread.length;
  const threadHref = `/mocks/threads/thread/${thread[0].id}`;

  $: n = Math.min(total, Math.max(1, Number($page.url.searchParams.get('n')) || 1));
  $: reed = thread[n - 1];
  $: prev = n > 1 ? thread[n - 2] : null;
  $: next = n < total ? thread[n] : null;
  $: reedReplies = replies[reed.id] ?? [];

  /** @param {number} target */
  function go(target) {
    if (target < 1 || target > total) return;
    void goto(`/mocks/threads/reed?n=${target}`, { noScroll: true, keepFocus: true });
  }

  /** @param {KeyboardEvent} e */
  function onKeydown(e) {
    const tag = /** @type {HTMLElement} */ (e.target)?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    if (e.key === 'ArrowLeft') go(n - 1);
    if (e.key === 'ArrowRight') go(n + 1);
  }
</script>

<svelte:window on:keydown={onKeydown} />

<Auth>
  <SideNav currentPage="reeds" />
  <div class="reed-detail-container">
    <div class="reed-content">
      <p class="mock-note">
        Threads mockup: reed detail. Use ← / → to move through the thread.
        <a href="/mocks/threads">All mockups</a>
      </p>

      <!-- Thread navigator: where this reed sits, and a way to step through. -->
      <nav class="thread-nav" aria-label="Thread navigation">
        <div class="thread-nav-top">
          <span class="thread-nav-title">
            <span class="thread-icon" aria-hidden="true"></span>
            Thread by <Username userID={author.userID} username={author.username} />
            <span aria-hidden="true">·</span>
            <a class="whole-link" href={threadHref}>View whole thread</a>
          </span>
          <span class="thread-nav-pager">
            <button type="button" class="pager-btn" on:click={() => go(n - 1)} disabled={!prev} aria-label="Previous reed in thread">‹</button>
            <span class="pager-pos">{n} of {total}</span>
            <button type="button" class="pager-btn" on:click={() => go(n + 1)} disabled={!next} aria-label="Next reed in thread">›</button>
          </span>
        </div>
        <div class="thread-progress">
          {#each thread as _, i}
            <button
              type="button"
              class="progress-seg"
              class:done={i + 1 < n}
              class:current={i + 1 === n}
              on:click={() => go(i + 1)}
              aria-label="Go to reed {i + 1}"
              aria-current={i + 1 === n ? 'true' : undefined}
            ></button>
          {/each}
        </div>
      </nav>

      {#if prev}
        <button type="button" class="neighbor prev" on:click={() => go(n - 1)}>
          <span class="neighbor-label">‹ Previous · {n - 1}/{total}</span>
          <span class="neighbor-text"><MarkdownParser text={prev.content} preview={true} /></span>
        </button>
        <div class="connector" aria-hidden="true"></div>
      {:else}
        <div class="thread-edge">Start of thread</div>
      {/if}

      <div class="reed-detail">
        <div class="reed-meta">
          <div class="reed-author">
            <a href="/profile/{author.userID}" class="author-avatar">
              <Avatar userID={author.userID} username={author.username} size="69px" />
            </a>
            <div class="author-info">
              <span class="author-line">
                <Username userID={author.userID} username={author.username} class="author-name" />
              </span>
              <p class="reed-date">{formatAbsoluteDateTime(reed.timestamp)}</p>
              <span class="reed-stats">
                {reed.stats.replies} replies · {reed.stats.echoes} echoes · {reed.stats.likes} likes · {reed.stats.coverage}%
              </span>
            </div>
          </div>
          <span class="position-chip">{n}/{total}</span>
        </div>

        <div class="reed-body">
          <MarkdownParser text={reed.content} />
        </div>

        <div class="reed-actions-bar">
          <button class="action-btn" type="button"><span class="action-icon icon-reply"></span><span class="action-label">Reply</span></button>
          <button class="action-btn" type="button"><span class="action-icon icon-echo"></span><span class="action-label">Echo</span></button>
          <button class="action-btn" type="button"><span class="action-icon icon-like"></span><span class="action-label">Like</span></button>
          <button class="action-btn" type="button"><span class="action-icon icon-share"></span><span class="action-label">Share</span></button>
        </div>
      </div>

      {#if next}
        <div class="connector" aria-hidden="true"></div>
        <button type="button" class="neighbor next" on:click={() => go(n + 1)}>
          <span class="neighbor-label">Next · {n + 1}/{total} ›</span>
          <span class="neighbor-text"><MarkdownParser text={next.content} preview={true} /></span>
        </button>
      {:else}
        <div class="thread-edge">End of thread</div>
      {/if}

      <!-- Replies belong to the focused reed, not to the thread as a whole. -->
      <div class="discussion-tabs" role="tablist">
        <button type="button" role="tab" class="discussion-tab active" aria-selected="true">
          Conversation{#if reedReplies.length}&nbsp;({reedReplies.length}){/if}
        </button>
        <button type="button" role="tab" class="discussion-tab" aria-selected="false">Ripples</button>
        <button type="button" role="tab" class="discussion-tab" aria-selected="false">Chorus</button>
      </div>
      <div class="replies">
        {#each reedReplies as r}
          <div class="reply-item">
            <ReedAuthorHeader userID={r.user.userID} username={r.user.username} subtext={formatRelativeTime(r.timestamp)} avatarSize="32px" />
            <div class="reply-body"><MarkdownParser text={r.content} /></div>
          </div>
        {:else}
          <p class="no-replies">No replies to {n}/{total} yet.</p>
        {/each}
      </div>
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>
</Auth>

<style>
  .reed-detail-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .reed-detail-container {
      padding-left: var(--sidenav-width);
    }
  }

  .reed-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
    box-sizing: border-box;
  }

  .mock-note {
    margin: 0 0 1rem;
    color: var(--muted);
    font-size: 0.8rem;
  }

  .mock-note a {
    color: var(--primary);
  }

  .thread-nav {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.6rem 1rem 0.75rem;
    margin-bottom: 0.75rem;
  }

  .thread-nav-top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
  }

  .thread-nav-title {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--muted);
    font-size: 0.9rem;
    min-width: 0;
  }

  .thread-icon {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    border-left: 2px solid currentColor;
    border-bottom: 2px solid currentColor;
    border-bottom-left-radius: 4px;
  }

  .thread-nav-pager {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .pager-pos {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.8rem;
    color: var(--fg);
    min-width: 3.5rem;
    text-align: center;
  }

  .pager-btn {
    width: 1.9rem;
    height: 1.9rem;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: none;
    color: var(--fg);
    font-size: 1.1rem;
    line-height: 1;
    cursor: pointer;
  }

  .pager-btn:hover:not(:disabled) {
    border-color: var(--primary);
  }

  .pager-btn:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }

  .thread-progress {
    display: flex;
    gap: 4px;
    margin-top: 0.6rem;
  }

  .progress-seg {
    flex: 1;
    height: 4px;
    padding: 0;
    border: none;
    border-radius: 2px;
    background: var(--border);
    cursor: pointer;
  }

  .progress-seg.done {
    background: color-mix(in srgb, var(--primary) 45%, var(--border));
  }

  .progress-seg.current {
    background: var(--primary);
  }

  .neighbor {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    width: 100%;
    text-align: left;
    padding: 0.6rem 1rem;
    background: transparent;
    border: 1px dashed var(--border);
    border-radius: 10px;
    color: var(--muted);
    font: inherit;
    cursor: pointer;
  }

  .neighbor:hover {
    border-color: var(--primary);
    color: var(--fg);
  }

  .neighbor-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--primary);
  }

  .neighbor.next .neighbor-label {
    align-self: flex-end;
  }

  .neighbor-text :global(.markdown-content) {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    font-size: 0.9rem;
  }

  .neighbor-text :global(p) {
    margin: 0;
  }

  .connector {
    width: 2px;
    height: 0.75rem;
    margin-left: calc(1rem + 34px);
    background: var(--border);
  }

  .thread-edge {
    text-align: center;
    color: var(--muted);
    font-size: 0.75rem;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    margin: 0.5rem 0;
  }

  .reed-detail {
    background: var(--surface);
    border: 1px solid var(--primary);
    border-radius: 12px;
  }

  .reed-meta {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-author {
    display: flex;
    align-items: stretch;
    gap: 1rem;
    min-width: 0;
  }

  .author-avatar {
    width: 69px;
    height: 69px;
    border-radius: 8px;
    overflow: hidden;
    display: flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
  }

  .author-info {
    min-width: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
  }

  .author-line :global(.author-name) {
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
    text-decoration: none;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .reed-stats {
    color: var(--muted);
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .position-chip {
    padding: 0.2rem 0.55rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 600;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .reed-body {
    padding: 0.75rem 1rem;
    word-break: break-word;
    font-size: 1.05rem;
  }

  .reed-actions-bar {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    border-top: 1px solid var(--border);
  }

  .action-btn {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    padding: 0.25rem 0.5rem;
    background: transparent;
    color: var(--fg);
  }

  .action-btn:hover {
    background: var(--input-bg);
  }

  .action-icon {
    display: inline-block;
    width: 1.2rem;
    height: 1.2rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .icon-echo {
    -webkit-mask-image: url('/icons/megaphone-24.png');
    mask-image: url('/icons/megaphone-24.png');
  }

  .icon-reply {
    -webkit-mask-image: url('/icons/reply-24.png');
    mask-image: url('/icons/reply-24.png');
  }

  .icon-share {
    -webkit-mask-image: url('/icons/share-24.png');
    mask-image: url('/icons/share-24.png');
  }

  .icon-like {
    -webkit-mask-image: url('/icons/like-24-outlined.png');
    mask-image: url('/icons/like-24-outlined.png');
  }

  .action-label {
    font-size: 0.75rem;
    color: var(--muted);
    font-weight: 500;
  }

  .whole-link {
    color: var(--primary);
    font-weight: 600;
    white-space: nowrap;
  }

  .discussion-tabs {
    display: flex;
    gap: 1.25rem;
    padding: 0 0.75rem;
    margin-top: 1rem;
  }

  .discussion-tab {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    padding: 0.6rem 0.1rem;
    font: inherit;
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--muted);
    cursor: pointer;
  }

  .discussion-tab.active {
    color: var(--fg);
    border-bottom-color: var(--primary);
  }

  .replies {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    margin-top: 0.75rem;
  }

  .reply-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.75rem 1rem;
  }

  .reply-body {
    margin-top: 0.5rem;
    word-break: break-word;
  }

  .no-replies {
    color: var(--muted);
    font-size: 0.9rem;
    text-align: center;
    margin: 1rem 0;
  }

  @media (max-width: 768px) {
    .reed-content {
      padding: 0.5rem;
    }

    .reed-meta,
    .reed-body {
      padding: 0.75rem;
    }
  }
</style>
