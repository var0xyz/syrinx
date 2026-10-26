<script>
  import { tick } from 'svelte';
  import { goto } from '$app/navigation';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import { MAX_REED_VISIBLE_CHARS, countMarkdownCharacters } from '$lib/utils/reedContent';
  import { notificationStore } from '$lib/stores/notifications';
  import { thread } from '../data.js';

  let open = true;
  let nextKey = 1;
  /** @type {{ key: number, text: string }[]} */
  let parts = [{ key: nextKey++, text: '' }];
  /** @type {HTMLTextAreaElement[]} */
  let textareas = [];

  $: isThread = parts.length > 1;
  $: counts = parts.map((p) => countMarkdownCharacters(p.text));
  $: overLimit = counts.map((c) => c > MAX_REED_VISIBLE_CHARS);
  $: anyEmpty = parts.some((p) => !p.text.trim());
  $: canPublish = !anyEmpty && !overLimit.some(Boolean);
  $: lastEmpty = !parts[parts.length - 1].text.trim();

  async function focusPart(i) {
    await tick();
    textareas[i]?.focus();
  }

  function addPart(after = parts.length - 1, text = '') {
    parts = [...parts.slice(0, after + 1), { key: nextKey++, text }, ...parts.slice(after + 1)];
    void focusPart(after + 1);
  }

  function removePart(i) {
    parts = parts.filter((_, j) => j !== i);
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
    const text = parts[i].text;
    const cut = splitPoint(text);
    parts[i].text = text.slice(0, cut).trimEnd();
    addPart(i, text.slice(cut).trimStart());
  }

  function onKeydown(e, i) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && e.shiftKey) {
      e.preventDefault();
      if (parts[i].text.trim()) addPart(i);
    } else if (e.key === 'Backspace' && !parts[i].text && parts.length > 1 && i > 0) {
      e.preventDefault();
      removePart(i);
    }
  }

  function publish() {
    notificationStore.info('Mockup only: nothing was signed or published.', 4000);
    open = false;
    void goto('/mocks/threads/feed');
  }

  function fillExample() {
    parts = [{
      key: nextKey++,
      text: thread.map((r) => r.content).join(' '),
    }];
    open = true;
    void focusPart(0);
  }

  function reset() {
    parts = [{ key: nextKey++, text: '' }];
    open = true;
  }
</script>

  <SideNav currentPage="reeds" />
  <div class="mock-container">
    <div class="mock-content">
      <div class="mock-banner">
        <strong>Threads mockup: composer.</strong>
        Type a reed, then use <em>Add to thread</em> (or ⌘⇧↵) to chain the next one.
        <div class="mock-banner-actions">
          <button type="button" on:click={reset}>Reset</button>
          <button type="button" on:click={fillExample}>Paste a long text</button>
          {#if !open}<button type="button" on:click={() => (open = true)}>Reopen composer</button>{/if}
          <a href="/mocks/threads">All mockups</a>
        </div>
      </div>
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>

  {#if open}
    <div class="modal-overlay" on:click={() => (open = false)} role="presentation"></div>
    <div class="write-modal">
      <div class="write-modal-header">
        <h2>{isThread ? 'New Thread' : 'New Reed'}</h2>
        {#if isThread}<span class="thread-count">{parts.length} reeds</span>{/if}
        <button class="close-btn" on:click={() => (open = false)} aria-label="Close">✕</button>
      </div>

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
                  bind:value={part.text}
                  on:keydown={(e) => onKeydown(e, i)}
                  rows={isThread ? 3 : 6}
                  placeholder={i === 0 ? "What's on your mind?" : 'Keep going…'}
                  aria-label={isThread ? `Reed ${i + 1} of ${parts.length}` : 'Reed'}
                ></textarea>
                <div class="part-info">
                  {#if overLimit[i]}
                    <button type="button" class="link-btn" on:click={() => splitOverflow(i)}>
                      Move overflow to a new reed
                    </button>
                  {:else if isThread}
                    <span class="part-position">{i + 1}/{parts.length}</span>
                  {:else}
                    <span></span>
                  {/if}
                  <span class="part-tools">
                    <span class="character-counter" class:over-limit={overLimit[i]}>
                      {counts[i]}/{MAX_REED_VISIBLE_CHARS}
                    </span>
                    {#if isThread && i > 0}
                      <button type="button" class="remove-btn" on:click={() => removePart(i)} aria-label="Remove reed {i + 1}">✕</button>
                    {/if}
                  </span>
                </div>
              </div>
            </li>
          {/each}
        </ol>

        <button type="button" class="add-btn" on:click={() => addPart()} disabled={lastEmpty}>
          <span class="add-icon" aria-hidden="true">+</span>
          {isThread ? 'Add another reed' : 'Add to thread'}
        </button>
        {#if !isThread}
          <p class="add-hint">Got more to say than {MAX_REED_VISIBLE_CHARS} characters? Chain reeds into a thread.</p>
        {/if}

        <div class="form-actions">
          <button type="button" class="btn btn-secondary" on:click={reset}>Discard</button>
          <button type="submit" class="btn btn-primary" disabled={!canPublish}>
            {isThread ? `Publish thread (${parts.length})` : 'Publish'}
          </button>
        </div>

        <div class="reed-preview">
          <div class="reed-preview-label">Preview</div>
          {#each parts as part, i (part.key)}
            <div class="reed-preview-card" class:chained={isThread && i < parts.length - 1}>
              {#if isThread}<span class="preview-badge">{i + 1}/{parts.length}</span>{/if}
              <div class="reed-preview-body">
                {#if part.text.trim()}
                  <MarkdownParser text={part.text} preview={true} />
                {:else}
                  <p class="reed-preview-empty">{i === 0 ? 'Your reed will appear here as you type.' : 'Empty reed'}</p>
                {/if}
              </div>
            </div>
          {/each}
          <p class="reed-format-hint">
            Each reed in a thread is signed and published on its own, so each can be replied to, echoed and liked separately.
          </p>
        </div>
      </form>
    </div>
  {/if}

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
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .mock-banner strong {
    color: var(--fg);
  }

  .mock-banner-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
    margin-top: 0.75rem;
  }

  .mock-banner-actions button {
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    cursor: pointer;
    font-size: 0.85rem;
  }

  .mock-banner-actions a {
    color: var(--primary);
    font-size: 0.85rem;
  }

  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 1001;
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
    overflow-y: auto;
    box-sizing: border-box;
  }

  @media (min-width: 900px) {
    .write-modal {
      width: 420px;
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
    margin: 0;
    font-size: 1.25rem;
    color: var(--fg);
  }

  .thread-count {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .close-btn {
    margin-left: auto;
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
  }

  .close-btn:hover {
    color: var(--fg);
    background: var(--border);
  }

  .parts {
    list-style: none;
    margin: 0;
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

  textarea {
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

  .is-thread textarea {
    min-height: 80px;
  }

  .part-info {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 0.35rem;
    min-height: 1.5rem;
    font-size: 0.8rem;
    color: var(--muted);
  }

  .part-tools {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .character-counter.over-limit {
    color: #ff6b6b;
  }

  .link-btn {
    background: none;
    border: none;
    padding: 0;
    color: var(--primary);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .remove-btn {
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

  .add-hint {
    margin: 0.5rem 0 0;
    font-size: 0.8rem;
    color: var(--muted);
  }

  .form-actions {
    display: flex;
    gap: 1rem;
    justify-content: space-between;
    margin-top: 1.25rem;
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
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
  }

  .reed-format-hint {
    margin: 1rem 0 0;
    color: var(--muted);
    font-size: 0.8rem;
    line-height: 1.4;
  }
</style>
