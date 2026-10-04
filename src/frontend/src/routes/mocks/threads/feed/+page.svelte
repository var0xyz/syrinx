<script>
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { formatRelativeTime } from '$lib/utils/time';
  import { author, thread, feedReeds } from '../data.js';

  const [first, second] = thread;
  const hiddenCount = thread.length - 2;
  const surfaced = thread[2];
  const threadHref = `/mocks/threads/thread/${first.id}`;

  /** @param {number} n */
  function open(n) {
    void goto(`/mocks/threads/reed?n=${n}`);
  }
</script>

<Auth>
  <SideNav currentPage="reeds" />
  <div class="mock-container">
    <div class="mock-content">
      <div class="mock-banner">
        <strong>Threads mockup: feed.</strong>
        A thread shows once, as its first reed plus a peek at the next.
        A single reed from a thread, surfaced on its own, says where it sits.
        <a href="/mocks/threads">All mockups</a>
      </div>

      <div class="list">
        <!-- Plain reed for contrast. -->
        <div class="reed-item">
          <div class="reed-header">
            <ReedAuthorHeader userID={feedReeds[0].user.userID} username={feedReeds[0].user.username} nameTag="h3" subtext={formatRelativeTime(feedReeds[0].timestamp)} linked={false} />
          </div>
          <div class="reed-preview"><MarkdownParser text={feedReeds[0].content} preview={true} /></div>
        </div>

        <!-- A whole thread, collapsed. -->
        <div class="reed-item thread-item" role="button" tabindex="0" on:click={() => open(1)} on:keydown={(e) => e.key === 'Enter' && open(1)}>
          <div class="reed-header">
            <ReedAuthorHeader userID={author.userID} username={author.username} nameTag="h3" subtext={formatRelativeTime(first.timestamp)} linked={false} />
            <span class="thread-chip" title="This reed starts a thread">
              <span class="thread-chip-icon" aria-hidden="true"></span>
              Thread · {thread.length}
            </span>
          </div>
          <div class="thread-row first">
            <div class="rail" aria-hidden="true"><span class="rail-dot">1</span><span class="rail-line"></span></div>
            <div class="thread-row-body"><MarkdownParser text={first.content} preview={true} /></div>
          </div>
          <div class="thread-row next" role="button" tabindex="0" on:click|stopPropagation={() => open(2)} on:keydown|stopPropagation={(e) => e.key === 'Enter' && open(2)}>
            <div class="rail" aria-hidden="true"><span class="rail-dot small">2</span><span class="rail-line dotted"></span></div>
            <div class="thread-row-body clamp"><MarkdownParser text={second.content} preview={true} /></div>
          </div>
          <div class="thread-more">
            <div class="rail" aria-hidden="true"><span class="rail-dots">⋮</span></div>
            <a class="thread-more-link" href={threadHref} on:click|stopPropagation>Read the whole thread · {hiddenCount} more</a>
          </div>
        </div>

        <!-- One reed from a thread, surfaced on its own (echo, like, mention...). -->
        <div class="reed-item" role="button" tabindex="0" on:click={() => open(3)} on:keydown={(e) => e.key === 'Enter' && open(3)}>
          <div class="reed-header">
            <ReedAuthorHeader userID={author.userID} username={author.username} nameTag="h3" subtext={formatRelativeTime(surfaced.timestamp)} linked={false} />
            <span class="position-chip">3/{thread.length}</span>
          </div>
          <div class="thread-context">
            <span class="thread-chip-icon" aria-hidden="true"></span>
            Part of a <a class="context-link" href={threadHref} on:click|stopPropagation>thread</a>: <em>“{first.content.slice(0, 48)}…”</em>
          </div>
          <div class="reed-preview"><MarkdownParser text={surfaced.content} preview={true} /></div>
        </div>

        <div class="reed-item">
          <div class="reed-header">
            <ReedAuthorHeader userID={feedReeds[1].user.userID} username={feedReeds[1].user.username} nameTag="h3" subtext={formatRelativeTime(feedReeds[1].timestamp)} linked={false} />
          </div>
          <div class="reed-preview"><MarkdownParser text={feedReeds[1].content} preview={true} /></div>
        </div>
      </div>
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>
</Auth>

<style>
  .mock-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .mock-container {
      padding-left: var(--sidenav-width);
    }
  }

  .mock-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .mock-banner {
    border: 1px dashed var(--border);
    border-radius: 12px;
    padding: 1rem;
    margin-bottom: 1rem;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .mock-banner strong {
    color: var(--fg);
  }

  .mock-banner a {
    color: var(--primary);
  }

  .list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .reed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    transition: all 0.2s ease;
    cursor: pointer;
  }

  .reed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .reed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
  }

  .thread-chip,
  .position-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.2rem 0.55rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 600;
    white-space: nowrap;
  }

  .position-chip {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .thread-chip-icon {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    border-left: 2px solid currentColor;
    border-bottom: 2px solid currentColor;
    border-bottom-left-radius: 4px;
  }

  .thread-row {
    display: flex;
    gap: 0.75rem;
    padding: 0 1rem;
  }

  .thread-row.first {
    padding-top: 1rem;
  }

  .thread-row-body {
    flex: 1;
    min-width: 0;
    padding-bottom: 0.75rem;
    word-break: break-word;
  }

  .thread-row.next .thread-row-body {
    color: var(--muted);
  }

  .thread-row.next:hover .thread-row-body {
    color: var(--fg);
  }

  .clamp :global(.markdown-content) {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .clamp :global(p) {
    margin: 0;
  }

  .rail {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 1.5rem;
    flex-shrink: 0;
  }

  .rail-dot {
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    font-size: 0.75rem;
    font-weight: 700;
    display: flex;
    align-items: center;
    justify-content: center;
    margin-top: 0.85rem;
  }

  .rail-dot.small {
    margin-top: 0;
    background: var(--border);
    color: var(--fg);
  }

  .rail-line {
    flex: 1;
    width: 2px;
    min-height: 0.75rem;
    margin-top: 0.25rem;
    background: var(--border);
  }

  .rail-line.dotted {
    background: none;
    border-left: 2px dotted var(--border);
    width: 0;
  }

  .rail-dots {
    color: var(--muted);
    line-height: 1;
  }

  .thread-more {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    padding: 0 1rem 1rem;
  }

  .thread-more-link {
    color: var(--primary);
    text-decoration: none;
    font-size: 0.9rem;
    font-weight: 600;
  }

  .thread-context {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.75rem 1rem 0;
    color: var(--muted);
    font-size: 0.8rem;
    min-width: 0;
  }

  .context-link {
    color: var(--primary);
  }

  .thread-context em {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 768px) {
    .mock-content {
      padding: 0.5rem;
    }

    .reed-header {
      padding: 0.75rem;
    }
  }
</style>
