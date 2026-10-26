<script>
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { formatAbsoluteDateTime, formatRelativeTime } from '$lib/utils/time';
  import { author, thread } from '../../data.js';

  const total = thread.length;
</script>

  <SideNav currentPage="reeds" />
  <div class="thread-container">
    <div class="thread-content">
      <p class="mock-note">
        Threads mockup: thread page. <a href="/mocks/threads">All mockups</a>
      </p>

      <header class="thread-header">
        <a href="/profile/{author.userID}" class="author-avatar">
          <Avatar userID={author.userID} username={author.username} size="56px" />
        </a>
        <div class="thread-header-info">
          <span class="thread-kicker">
            <span class="thread-icon" aria-hidden="true"></span>
            Thread · {total} reeds
          </span>
          <span class="thread-author">
            <Username userID={author.userID} username={author.username} class="author-name" />
          </span>
          <span class="thread-date">Started {formatAbsoluteDateTime(thread[0].timestamp)}</span>
        </div>
      </header>

      <p class="read-only-note">
        This page is for reading. To reply, echo or like, open the reed you want to respond to.
      </p>

      <ol class="thread-list">
        {#each thread as reed, i}
          <li class="thread-entry">
            <div class="rail" aria-hidden="true">
              <span class="rail-dot">{i + 1}</span>
              {#if i < total - 1}<span class="rail-line"></span>{/if}
            </div>
            <a class="thread-reed" href="/mocks/threads/reed?n={i + 1}">
              <span class="thread-reed-meta">
                <span>{i + 1}/{total} · {formatRelativeTime(reed.timestamp)}</span>
                <span class="thread-reed-stats">
                  {reed.stats.replies} replies · {reed.stats.likes} likes
                </span>
              </span>
              <span class="thread-reed-body"><MarkdownParser text={reed.content} preview={true} /></span>
              <span class="thread-reed-open">Open reed ›</span>
            </a>
          </li>
        {/each}
      </ol>

      <div class="thread-edge">End of thread</div>
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>

<style>
  .thread-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .thread-container {
      padding-left: var(--sidenav-width);
    }
  }

  .thread-content {
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

  .thread-header {
    display: flex;
    gap: 1rem;
    align-items: stretch;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.75rem 1rem;
  }

  .author-avatar {
    width: 56px;
    height: 56px;
    border-radius: 8px;
    overflow: hidden;
    display: flex;
    flex-shrink: 0;
  }

  .thread-header-info {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    min-width: 0;
  }

  .thread-kicker {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .thread-icon {
    display: inline-block;
    width: 0.7rem;
    height: 0.7rem;
    border-left: 2px solid currentColor;
    border-bottom: 2px solid currentColor;
    border-bottom-left-radius: 4px;
  }

  .thread-author :global(.author-name) {
    color: var(--fg);
    font-size: 1.15rem;
    font-weight: 600;
    text-decoration: none;
  }

  .thread-date {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .read-only-note {
    margin: 0.75rem 0 1rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .thread-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .thread-entry {
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
    margin-top: 0.75rem;
  }

  .rail-line {
    flex: 1;
    width: 2px;
    margin-top: 0.25rem;
    background: var(--border);
  }

  .thread-reed {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    margin-bottom: 0.75rem;
    padding: 0.75rem 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    color: var(--fg);
    text-decoration: none;
    transition: border-color 0.2s ease, box-shadow 0.2s ease;
  }

  .thread-reed:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .thread-reed-meta {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
    color: var(--muted);
    font-size: 0.75rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .thread-reed-body {
    word-break: break-word;
    font-size: 1.05rem;
  }

  .thread-reed-body :global(p) {
    margin: 0;
  }

  .thread-reed-open {
    align-self: flex-end;
    color: var(--primary);
    font-size: 0.8rem;
    font-weight: 600;
    opacity: 0;
    transition: opacity 0.2s ease;
  }

  .thread-reed:hover .thread-reed-open,
  .thread-reed:focus-visible .thread-reed-open {
    opacity: 1;
  }

  @media (hover: none) {
    .thread-reed-open {
      opacity: 1;
    }
  }

  .thread-edge {
    text-align: center;
    color: var(--muted);
    font-size: 0.75rem;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    margin: 0.5rem 0 1.5rem;
  }

  @media (max-width: 768px) {
    .thread-content {
      padding: 0.5rem;
    }
  }
</style>
