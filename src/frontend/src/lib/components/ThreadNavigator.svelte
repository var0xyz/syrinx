<script>
  import { goto } from '$app/navigation';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { loadThread } from '$lib/services/threadFetch';

  /** @type {import('$lib/types/reed').ReedType} */
  export let reed;
  /** Above the reed (navigator and previous part) or below it (next part). */
  /** @type {'top' | 'bottom'} */
  export let where = 'top';
  /** The thread when the page already holds it, so nothing waits to paint. */
  /** @type {import('$lib/services/threadFetch').HeldThread | null} */
  export let thread = null;

  /** @type {import('$lib/types/reed').ReedType[]} */
  let parts = [];

  let shownHead = '';

  $: head = reed?.thread?.head;
  $: void show(head, thread);

  /**
   * @param {string | undefined} threadID
   * @param {import('$lib/services/threadFetch').HeldThread | null} held
   */
  async function show(threadID, held) {
    if (!threadID) {
      parts = [];
      shownHead = '';
      return;
    }
    if (held?.record.threadId === threadID) {
      parts = held.reeds;
      shownHead = threadID;
      return;
    }
    // Moving within the thread already shown keeps it up; nothing flashes.
    if (threadID === shownHead) return;
    parts = [];
    try {
      const loaded = await loadThread(threadID);
      if (threadID !== head) return;
      parts = loaded.reeds;
      shownHead = threadID;
    } catch {
      parts = [];
    }
  }

  $: n = (reed?.thread?.index ?? 0) + 1;
  $: total = parts.length;
  $: prev = total && n > 1 ? parts[n - 2] : null;
  $: next = total && n < total ? parts[n] : null;
  $: threadHref = head ? `/thread/${head}` : '';

  /** @param {number} target */
  function go(target) {
    if (target < 1 || target > total) return;
    void goto(`/reed/${parts[target - 1].id}`, { noScroll: true, keepFocus: true });
  }
</script>

{#if reed?.thread}
  {#if where === 'top'}
    <nav class="thread-nav" aria-label="Thread navigation">
      <div class="thread-nav-top">
        <span class="thread-nav-title">
          <span class="thread-icon" aria-hidden="true"></span>
          Thread
          <span aria-hidden="true">·</span>
          <a class="whole-link" href={threadHref}>Expand</a>
        </span>
        {#if total}
          <span class="thread-nav-pager">
            <button type="button" class="pager-btn" on:click={() => go(n - 1)} disabled={!prev} aria-label="Previous reed in thread">
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M10 3 5 8l5 5" /></svg>
            </button>
            <span class="pager-pos">{n} of {total}</span>
            <button type="button" class="pager-btn" on:click={() => go(n + 1)} disabled={!next} aria-label="Next reed in thread">
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M6 3l5 5-5 5" /></svg>
            </button>
          </span>
        {/if}
      </div>
      {#if total}
        <div class="thread-progress">
          {#each parts as _, i}
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
      {/if}
    </nav>

    {#if prev}
      <button type="button" class="neighbor prev" on:click={() => go(n - 1)}>
        <span class="neighbor-label">‹ Previous</span>
        <span class="neighbor-text"><MarkdownParser text={prev.content} preview={true} /></span>
      </button>
      <div class="connector" aria-hidden="true"></div>
    {:else if total}
      <div class="thread-edge">Start of thread</div>
    {/if}
  {:else if next}
    <div class="connector" aria-hidden="true"></div>
    <button type="button" class="neighbor next" on:click={() => go(n + 1)}>
      <span class="neighbor-label">Next ›</span>
      <span class="neighbor-text"><MarkdownParser text={next.content} preview={true} /></span>
    </button>
  {:else if total}
    <div class="thread-edge">End of thread</div>
  {/if}
{/if}

<style>
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
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: none;
    color: var(--fg);
    cursor: pointer;
  }

  .pager-btn svg {
    fill: none;
    stroke: currentColor;
    stroke-width: 1.75;
    stroke-linecap: round;
    stroke-linejoin: round;
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


  .whole-link {
    color: var(--primary);
    font-weight: 600;
    white-space: nowrap;
  }
</style>
