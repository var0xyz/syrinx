<script>
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import Username from '$lib/components/Username.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import TrustMark from '$lib/components/TrustMark.svelte';
  import { formatAbsoluteDateTime } from '$lib/utils/time';
  import { getLocalThread, loadThread } from '$lib/services/threadFetch';
  import { threadsRepository } from '$lib/repositories/threads';
  import { userRepository } from '$lib/repositories/user';

  /** @type {import('$lib/services/threadFetch').HeldThread | null} */
  let thread = null;
  let username = '';
  let loading = true;
  let failure = '';

  $: threadID = $page.params.threadID;
  $: void load(threadID);

  /** @param {string} id */
  async function load(id) {
    thread = null;
    failure = '';
    loading = true;
    try {
      if (await threadsRepository.isRemoved(id)) {
        failure = 'This thread was deleted by its author.';
        return;
      }
      // Render what this device holds at once; fetch only what's missing.
      thread = (await getLocalThread(id)) ?? (await loadThread(id));
      const author = await userRepository.get(thread.record.userID).catch(() => null);
      username = author?.username ?? thread.record.userID;
    } catch {
      failure = "This thread couldn't be loaded. Nobody holding it is online right now.";
    } finally {
      loading = false;
    }
  }

  $: total = thread?.reeds.length ?? 0;
</script>

<Auth>
  <SideNav currentPage="reeds" />
  <div class="thread-container">
    <div class="thread-content">
      {#if thread}
        <!-- Laid out like the reed detail's header, the thread size where its stats go. -->
        <div class="thread-header">
          <div class="reed-author">
            <a href="/profile/{thread.record.userID}" class="author-avatar">
              <Avatar userID={thread.record.userID} {username} size="69px" />
            </a>
            <div class="author-info">
              <span class="author-line">
                <Username userID={thread.record.userID} {username} class="author-name" />
                <TrustMark userID={thread.record.userID} linked={false} refresh />
              </span>
              <p class="reed-date">{formatAbsoluteDateTime(thread.record.serverSignature.timestamp)}</p>
              <span class="thread-stats">
                <span class="thread-icon" aria-hidden="true"></span>
                Thread · {total} reeds
              </span>
            </div>
          </div>
        </div>

        <ol class="thread-list">
          {#each thread.reeds as reed, i (reed.id)}
            <li class="thread-entry">
              <div class="rail" aria-hidden="true">
                <span class="rail-dot">{i + 1}</span>
                {#if i < total - 1}<span class="rail-line"></span>{/if}
              </div>
              <a class="thread-reed" href="/reed/{reed.id}">
                <!-- Mobile only: the rail carries the position on wider screens. -->
                <span class="thread-reed-position">{i + 1}/{total}</span>
                <span class="thread-reed-body"><MarkdownParser text={reed.content} preview={true} /></span>
              </a>
            </li>
          {/each}
        </ol>

        <div class="thread-edge">End of thread</div>
      {:else if loading}
        <p class="status-note">Loading thread…</p>
      {:else}
        <p class="status-note">{failure}</p>
      {/if}
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>
</Auth>

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

  .thread-header {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.75rem 1rem;
    margin-bottom: 1rem;
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
    text-decoration: none;
  }

  .author-info {
    min-width: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
  }

  .author-line {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    min-width: 0;
  }

  .author-line :global(.author-name) {
    color: var(--fg);
    font-size: 1.2rem;
    font-weight: 600;
    text-decoration: none;
    word-break: break-word;
  }

  .reed-date {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .thread-stats {
    min-height: 1rem;
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    color: var(--muted);
    font-size: 0.7rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    letter-spacing: 0.02em;
    opacity: 0.8;
  }

  .status-note {
    margin: 0.75rem 0 1rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .thread-reed-position {
    display: none;
    color: var(--muted);
    font-size: 0.75rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .thread-icon {
    display: inline-block;
    width: 0.7rem;
    height: 0.7rem;
    border-left: 2px solid currentColor;
    border-bottom: 2px solid currentColor;
    border-bottom-left-radius: 4px;
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

  .thread-reed-body {
    word-break: break-word;
    font-size: 1.05rem;
  }

  .thread-reed-body :global(p) {
    margin: 0;
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

    .thread-header {
      padding: 0.75rem;
    }

    /* The big numbered rail is for wide screens; here the reed shows its place. */
    .rail {
      display: none;
    }

    .thread-entry {
      gap: 0;
    }

    .thread-reed-position {
      display: block;
    }
  }
</style>
