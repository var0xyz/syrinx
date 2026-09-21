<script>
  import { page } from '$app/stores';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import { formatAbsoluteDateTime } from '$lib/utils/time';

  let isComposeOpen = false;

  // Coarse top-level hint for routes the URL alone can't map to a nav
  // destination (reed detail, pipe, mesh peer/attempt, error page) — leave
  // unset to derive purely from the URL. Never implies a sub-item.
  /** @type {'reeds' | 'feeds' | 'lists' | 'interactions' | 'network' | 'invites' | 'account' | ''} */
  export let currentPage = '';

  $: path = $page.url.pathname;
  $: isAdmin =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: ownProfileHref = $page.data?.user ? `/profile/${$page.data.user.id}` : '/reeds';

  $: onOwnProfile = $page.data?.user && path === `/profile/${$page.data.user.id}`;
  $: reedsActive = currentPage === 'reeds' || onOwnProfile || path === '/reeds/saved';
  $: profileSubActive = onOwnProfile;
  $: likedSubActive = path === '/reeds/saved';

  $: feedsActive = currentPage === 'feeds' || path.startsWith('/feed/follow') || path === '/feed/broadcast' || path === '/feed/pipes';
  $: followSubActive = path.startsWith('/feed/follow');
  $: broadcastSubActive = path === '/feed/broadcast';
  $: pipesSubActive = path === '/feed/pipes';

  $: listsActive = currentPage === 'lists' || path.startsWith('/feed/lists');

  $: interactionsActive = currentPage === 'interactions' || path === '/replies' || path === '/ripples' || path === '/feed/mentions';
  $: repliesSubActive = path === '/replies';
  $: ripplesSubActive = path === '/ripples';
  $: mentionsSubActive = path === '/feed/mentions';

  $: networkActive = currentPage === 'network' || path.startsWith('/network/');
  $: usersSubActive = path.startsWith('/network/users');
  $: meshSubActive = path === '/network/mesh';

  $: invitesActive = currentPage === 'invites' || path === '/invites';

  $: accountActive = currentPage === 'account' || path === '/account';
</script>

<nav class="side-nav">
  <button type="button" class="sn-compose" on:click={() => (isComposeOpen = true)}>
    <span class="sn-compose-icon"></span>New Reed
  </button>

  <a href={ownProfileHref} class="sn-btn" class:active={reedsActive}>
    <span class="sn-icon">🌾</span>Reeds
  </a>
  <a href={ownProfileHref} class="sn-sub" class:active={profileSubActive}>
    <span class="sn-dot"></span>Mine
  </a>
  <a href="/reeds/saved" class="sn-sub" class:active={likedSubActive}>
    <span class="sn-dot"></span>Liked
  </a>

  <a href="/feed/follow" class="sn-btn" class:active={feedsActive}>
    <span class="sn-icon">📰</span>Feeds
  </a>
  <a href="/feed/follow" class="sn-sub" class:active={followSubActive}>
    <span class="sn-dot"></span>Following
  </a>
  <a href="/feed/broadcast" class="sn-sub" class:active={broadcastSubActive}>
    <span class="sn-dot"></span>Broadcast
  </a>
  <a href="/feed/pipes" class="sn-sub" class:active={pipesSubActive}>
    <span class="sn-dot"></span>Pipes
  </a>

  <a href="/feed/lists" class="sn-btn" class:active={listsActive}>
    <span class="sn-icon">📋</span>Lists
  </a>

  <a href="/replies" class="sn-btn" class:active={interactionsActive}>
    <span class="sn-icon">💬</span>Interactions
  </a>
  <a href="/replies" class="sn-sub" class:active={repliesSubActive}>
    <span class="sn-dot"></span>Replies
  </a>
  <a href="/ripples" class="sn-sub" class:active={ripplesSubActive}>
    <span class="sn-dot"></span>Ripples
  </a>
  <a href="/feed/mentions" class="sn-sub" class:active={mentionsSubActive}>
    <span class="sn-dot"></span>Mentions
  </a>

  {#if isAdmin}
    <a href="/network/users" class="sn-btn" class:active={networkActive}>
      <span class="sn-icon">🌐</span>Network
    </a>
    <a href="/network/users" class="sn-sub" class:active={usersSubActive}>
      <span class="sn-dot"></span>Users
    </a>
    <a href="/network/mesh" class="sn-sub" class:active={meshSubActive}>
      <span class="sn-dot"></span>Mesh
    </a>
  {:else}
    <a href="/invites" class="sn-btn" class:active={invitesActive}>
      <span class="sn-icon">✉️</span>Invites
    </a>
  {/if}

  <a href="/account" class="sn-btn sn-account" class:active={accountActive}>
    <span class="sn-icon">👤</span>
    <span class="sn-account-label">{$page.data?.user?.username ?? 'Account'}</span>
  </a>

  <p class="sn-version">
    Version {__APP_VERSION__.slice(0, 12)}<br />
    Built {formatAbsoluteDateTime(__APP_BUILD_TIME__)}
  </p>
</nav>

<NewReedModal open={isComposeOpen} on:close={() => (isComposeOpen = false)} />

<style>
  .side-nav {
    display: none;
  }

  @media (min-width: 768px) {
    .side-nav {
      display: flex;
      flex-direction: column;
      align-items: stretch;
      gap: 0.2rem;
      position: fixed;
      top: calc(3rem + 1px);
      left: 0;
      bottom: 0;
      width: var(--sidenav-width);
      background: var(--surface);
      border-right: 1px solid var(--border);
      padding: 1rem 0.7rem;
      overflow-y: auto;
      z-index: 90;
    }
  }

  .sn-compose {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.4rem;
    width: 100%;
    margin: 0 0 0.9rem;
    padding: 0.65rem 0.8rem;
    border: none;
    border-radius: 10px;
    background: var(--primary);
    color: var(--button-text);
    font-family: inherit;
    font-size: 0.85rem;
    font-weight: 700;
    cursor: pointer;
    transition: opacity 0.2s ease;
  }

  .sn-compose:hover {
    opacity: 0.9;
  }

  .sn-compose-icon {
    display: inline-block;
    width: 1rem;
    height: 1rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/quill-pen-24.png');
    mask-image: url('/icons/quill-pen-24.png');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
    flex-shrink: 0;
  }

  .sn-btn {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 0.6rem 0.7rem;
    border-radius: 10px;
    color: var(--muted);
    font-size: 0.82rem;
    font-weight: 600;
    text-decoration: none;
  }

  .sn-account {
    margin-top: auto;
  }

  .sn-account-label {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
    min-width: 0;
  }

  .sn-btn:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .sn-btn.active {
    background: rgba(88, 166, 255, 0.14);
    color: var(--fg);
  }

  .sn-icon {
    font-size: 1.15rem;
    flex-shrink: 0;
  }

  .sn-sub {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.42rem 0.7rem 0.42rem 2.1rem;
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.76rem;
    font-weight: 500;
    text-decoration: none;
  }

  .sn-sub:hover {
    color: var(--fg);
  }

  .sn-sub.active {
    color: var(--fg);
  }

  .sn-sub .sn-dot {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: currentColor;
    flex-shrink: 0;
  }

  .sn-version {
    margin: 0.6rem 0 0;
    padding: 0 0.7rem;
    color: var(--muted);
    font-size: 0.68rem;
    font-family: monospace;
    line-height: 1.4;
  }
</style>
