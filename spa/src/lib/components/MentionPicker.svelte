<script>
  import { onDestroy, tick } from 'svelte';
  import { apiService } from '$lib/services/api';

  /** The textarea element this picker is attached to. */
  export let textarea = null;

  /** Bound two-way: the composer's content string. */
  export let content = '';

  /**
   * Bound two-way: userID -> username for mentions confirmed this compose
   * session. Search results are unsigned (GET /users/search is a plain
   * typeahead, not a verified profile), so this is display-only — never
   * persisted, never substituted for the real verified fetch MentionLink
   * still performs. It only lets the composer preview show the picked
   * username immediately instead of the raw id while that fetch is in flight.
   */
  export let usernameHints = new Map();

  const MAX_USERNAME_LENGTH = 32;
  const DEBOUNCE_MS = 200;

  let open = false;
  let query = '';
  let mentionStart = -1; // index of '@' in content
  let results = [];
  let highlighted = 0;
  let loading = false;
  let popoverStyle = '';

  let debounceTimeout = null;
  let currentSearch = null;

  function closePicker() {
    open = false;
    query = '';
    mentionStart = -1;
    results = [];
    highlighted = 0;
    if (debounceTimeout) {
      clearTimeout(debounceTimeout);
      debounceTimeout = null;
    }
    if (currentSearch) {
      currentSearch.abort();
      currentSearch = null;
    }
  }

  /** Call after every content/caret change (on:input, on:click, on:keyup). */
  export function handleCaretChange() {
    if (!textarea) return;
    const caret = textarea.selectionStart ?? content.length;

    if (open) {
      // Still inside the active run? content between mentionStart and caret
      // must start with '@' and contain no newline.
      const run = content.slice(mentionStart, caret);
      if (
        mentionStart < 0 ||
        caret < mentionStart ||
        !run.startsWith('@') ||
        run.includes('\n')
      ) {
        closePicker();
      } else {
        setQuery(run.slice(1));
        return;
      }
    }

    // Not open (or just closed above): does the caret sit right after a
    // fresh '@' that starts a mention run? Only trigger on the '@' itself
    // being the most recent unconsumed trigger — i.e. caret - 1 is '@' and
    // the char before that is start-of-string or whitespace (so "email@x"
    // does not trigger a mention on every keystroke).
    const priorChar = caret >= 2 ? content[caret - 2] : undefined;
    if (content[caret - 1] === '@' && (caret === 1 || priorChar === undefined || /\s/.test(priorChar))) {
      mentionStart = caret - 1;
      open = true;
      highlighted = 0;
      results = [];
      setQuery('');
      positionPopover();
    }
  }

  function setQuery(q) {
    query = q;
    if (query.length > MAX_USERNAME_LENGTH && results.length === 0) {
      closePicker();
      return;
    }
    debouncedSearch();
  }

  function debouncedSearch() {
    if (debounceTimeout) clearTimeout(debounceTimeout);
    if (currentSearch) {
      currentSearch.abort();
      currentSearch = null;
    }
    if (query.trim().length === 0) {
      results = [];
      loading = false;
      return;
    }
    loading = true;
    debounceTimeout = setTimeout(runSearch, DEBOUNCE_MS);
  }

  async function runSearch() {
    const controller = new AbortController();
    currentSearch = controller;
    const q = query;
    try {
      const res = await apiService.searchUsers(q, 20);
      if (controller.signal.aborted) return;
      results = res.users || [];
      highlighted = 0;
      loading = false;
      // Auto-cancel: query has run past the max possible username length
      // and still nothing matches — the user has typed past any real @.
      if (results.length === 0 && q.length > MAX_USERNAME_LENGTH) {
        closePicker();
      }
    } catch (err) {
      if (controller.signal.aborted) return;
      loading = false;
      results = [];
    } finally {
      if (currentSearch === controller) currentSearch = null;
    }
  }

  async function positionPopover() {
    await tick();
    if (!textarea) return;
    const coords = caretCoordinates(textarea, mentionStart);
    const rect = textarea.getBoundingClientRect();
    const top = rect.top + coords.top + coords.lineHeight - textarea.scrollTop;
    const left = rect.left + coords.left - textarea.scrollLeft;
    popoverStyle = `top: ${top}px; left: ${left}px;`;
  }

  function confirm(user) {
    if (!textarea || !user) return;
    const caret = textarea.selectionStart ?? content.length;
    // The '@' that triggered the picker is replaced entirely — it's SPA-
    // only compose-time UI, not part of the signed content. The wire form
    // is ~userID@serverID (see reedMarkdown.ts readMention) — no username,
    // no link syntax, so it's immune to spaces in usernames. Rendering
    // resolves userID -> username and links to the profile.
    const before = content.slice(0, mentionStart);
    const after = content.slice(caret);
    // user.id from GET /users/search is already canonical (userID@serverID,
    // see services.go's SearchUsers) — do not re-append the server suffix,
    // that's what produced the doubled "@serverID@serverID" bug.
    let token = `~${user.id}`;
    // Guarantee a boundary so the token can't fuse with adjacent text and
    // get misread as a longer/invalid ID run (see readMention's boundary
    // check) — only pad when the next character would otherwise continue
    // the ID alphabet or chain another '@'.
    if (after.length === 0 || /[a-zA-Z0-9@]/.test(after[0])) {
      token += ' ';
    }
    const nextContent = before + token + after;
    const newCaret = before.length + token.length;
    content = nextContent;
    if (user.username) {
      usernameHints.set(user.id, user.username);
      usernameHints = usernameHints; // trigger the two-way bind reassignment
    }
    closePicker();
    tick().then(() => {
      textarea.focus();
      textarea.setSelectionRange(newCaret, newCaret);
    });
  }

  function handleKeydown(e) {
    if (!open) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closePicker();
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (results.length > 0) highlighted = (highlighted + 1) % results.length;
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (results.length > 0) highlighted = (highlighted - 1 + results.length) % results.length;
      return;
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      if (results.length > 0) {
        e.preventDefault();
        confirm(results[highlighted]);
      }
      return;
    }
  }

  function handleWindowClick(e) {
    if (!open) return;
    const popover = document.getElementById('mention-picker-popover');
    if (popover && (popover === e.target || popover.contains(e.target))) return;
    if (textarea && (textarea === e.target)) return;
    closePicker();
  }

  /**
   * Mirror-div technique: clone the textarea's box/font metrics into an
   * offscreen div holding the same text up to `index`, then read the
   * position of a marker span at that point. Standard approach for
   * getting pixel caret coordinates inside a <textarea>.
   */
  function caretCoordinates(el, index) {
    const style = window.getComputedStyle(el);
    const mirror = document.createElement('div');
    const props = [
      'boxSizing', 'width', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
      'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
      'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'letterSpacing',
      'lineHeight', 'textTransform', 'wordSpacing', 'whiteSpace', 'wordWrap',
    ];
    for (const p of props) mirror.style[p] = style[p];
    mirror.style.position = 'absolute';
    mirror.style.visibility = 'hidden';
    mirror.style.whiteSpace = 'pre-wrap';
    mirror.style.wordWrap = 'break-word';
    mirror.style.top = '0';
    mirror.style.left = '-9999px';
    mirror.style.height = 'auto';

    const before = el.value.slice(0, index);
    mirror.textContent = before;
    const marker = document.createElement('span');
    marker.textContent = '​';
    mirror.appendChild(marker);

    document.body.appendChild(mirror);
    const top = marker.offsetTop;
    const left = marker.offsetLeft;
    const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.2;
    document.body.removeChild(mirror);

    return { top, left, lineHeight };
  }

  onDestroy(() => {
    closePicker();
  });
</script>

<svelte:window on:click={handleWindowClick} on:keydown={handleKeydown} />

{#if open}
  <div id="mention-picker-popover" class="mention-popover" style={popoverStyle} role="listbox">
    {#if loading}
      <div class="mention-status">Searching…</div>
    {:else if results.length === 0}
      <div class="mention-status">No matches</div>
    {:else}
      {#each results as user, i}
        <button
          type="button"
          class="mention-result"
          class:highlighted={i === highlighted}
          on:mouseenter={() => (highlighted = i)}
          on:click={() => confirm(user)}
        >
          {user.username}<span class="mention-result-server">· {user.serverName}</span>
        </button>
      {/each}
    {/if}
  </div>
{/if}

<style>
  .mention-popover {
    position: fixed;
    z-index: 1100;
    min-width: 160px;
    max-width: 280px;
    max-height: 220px;
    overflow-y: auto;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
    padding: 0.25rem;
  }

  .mention-status {
    padding: 0.5rem 0.75rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .mention-result {
    display: block;
    width: 100%;
    text-align: left;
    padding: 0.5rem 0.75rem;
    border: none;
    background: none;
    color: var(--fg);
    font-size: 0.9rem;
    border-radius: 6px;
    cursor: pointer;
  }

  .mention-result.highlighted,
  .mention-result:hover {
    background: var(--input-bg);
  }

  .mention-result-server {
    color: var(--muted);
    margin-left: 0.3em;
  }
</style>
