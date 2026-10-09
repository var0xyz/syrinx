<script>
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { threadsRepository } from '$lib/repositories/threads';

  /** @type {import('$lib/types/reed').ReedType} */
  export let reed;
  /** Where in a list item this renders: the header chip, a head's subtle
   * label atop the content, above the content (a part's context), or below
   * it (a head's peek and link). */
  /** @type {'chip' | 'label' | 'before' | 'after'} */
  export let where = 'chip';

  let total = 0;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let head = null;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let next = null;

  $: thread = reed?.thread;
  $: isHead = thread?.index === 0;
  $: href = thread ? `/thread/${thread.head}` : '';
  $: void load(thread?.head);

  /** @param {string | undefined} threadID */
  async function load(threadID) {
    if (!threadID) return;
    const summary = await threadsRepository.summary(threadID);
    total = summary.total;
    head = summary.head;
    next = summary.next;
  }

  $: hidden = Math.max(0, total - (next ? 2 : 1));
</script>

{#if thread}
  {#if where === 'chip'}
    {#if isHead}
      <span class="thread-chip" title="This reed starts a thread">
        <span class="thread-chip-icon" aria-hidden="true"></span>
        Thread{total ? ` · ${total}` : ''}
      </span>
    {:else}
      <span class="position-chip">{thread.index + 1}{total ? `/${total}` : ''}</span>
    {/if}
  {:else if where === 'label' && isHead}
    <span class="thread-label">Thread{total ? ` · ${total}` : ''}</span>
  {:else if where === 'before' && !isHead}
    <div class="thread-context">
      <span class="thread-chip-icon" aria-hidden="true"></span>
      <span>Thread{#if head}:{/if}</span>
      {#if head}<em>“{head.content.slice(0, 48)}…”</em>{/if}
    </div>
  {:else if where === 'after' && isHead}
    {#if next}
      <a class="thread-row next" href="/reed/{next.id}" on:click|stopPropagation>
        <div class="rail" aria-hidden="true">
          <span class="rail-dot small">2</span>
          {#if hidden}<span class="rail-line dotted"></span>{/if}
        </div>
        <div class="thread-row-body clamp"><MarkdownParser text={next.content} preview={true} /></div>
      </a>
    {/if}
    <div class="thread-more">
      <div class="rail" aria-hidden="true">{#if hidden}<span class="rail-dots">⋮</span>{/if}</div>
      <a class="thread-more-link" {href} on:click|stopPropagation>
        Read the whole thread
      </a>
    </div>
  {/if}
{/if}

<style>
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

  .thread-label {
    display: block;
    margin: 0.35rem 0;
    color: var(--muted);
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    letter-spacing: 0.02em;
    opacity: 0.8;
  }

  .thread-chip-icon {
    display: inline-block;
    flex-shrink: 0;
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
    color: inherit;
    text-decoration: none;
  }

  .thread-row-body {
    flex: 1;
    min-width: 0;
    padding-bottom: 0.75rem;
    word-break: break-word;
    color: var(--muted);
  }

  .thread-row:hover .thread-row-body {
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
    font-size: 0.75rem;
    font-weight: 700;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .rail-dot.small {
    background: var(--border);
    color: var(--fg);
  }

  .rail-line {
    flex: 1;
    min-height: 0.75rem;
    margin-top: 0.25rem;
  }

  .rail-line.dotted {
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

  .thread-context em {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
