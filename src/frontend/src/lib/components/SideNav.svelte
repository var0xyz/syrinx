<script>
  import { page } from '$app/stores';

  // Coarse top-level hint for routes the URL alone can't map to a nav
  // destination (reed detail, pipe, mesh peer/attempt, error page) — leave
  // unset to derive purely from the URL. Never implies a sub-item.
  /** @type {'reeds' | 'feeds' | 'network' | 'invites' | 'account' | ''} */
  export let currentPage = '';

  $: path = $page.url.pathname;
  $: isAdmin =
    $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
  $: ownProfileHref = $page.data?.user ? `/profile/${$page.data.user.id}` : '/reeds';

  $: onOwnProfile = $page.data?.user && path === `/profile/${$page.data.user.id}`;
  $: reedsActive = currentPage === 'reeds' || onOwnProfile || path === '/reeds/likes' || path === '/reeds/mentions';
  $: profileSubActive = onOwnProfile;
  $: likedSubActive = path === '/reeds/likes';
  $: mentionsSubActive = path === '/reeds/mentions';

  $: feedsActive = path.startsWith('/feed/follow') || path === '/feed/broadcast';
  $: followSubActive = path.startsWith('/feed/follow');
  $: broadcastSubActive = path === '/feed/broadcast';

  $: listsActive = path.startsWith('/feed/lists');

  $: networkActive = currentPage === 'network' || path.startsWith('/network/');
  $: usersSubActive = path.startsWith('/network/users');
  $: meshSubActive = path === '/network/mesh';

  $: invitesActive = currentPage === 'invites' || path === '/invites';

  $: accountActive = currentPage === 'account' || path === '/account';
</script>

<nav class="side-nav">
  <a href={ownProfileHref} class="sn-btn" class:active={reedsActive}>
    <span class="sn-icon">🌾</span>Reeds
  </a>
  <a href={ownProfileHref} class="sn-sub" class:active={profileSubActive}>
    <span class="sn-dot"></span>Profile
  </a>
  <a href="/reeds/likes" class="sn-sub" class:active={likedSubActive}>
    <span class="sn-dot"></span>Liked
  </a>
  <a href="/reeds/mentions" class="sn-sub" class:active={mentionsSubActive}>
    <span class="sn-dot"></span>Mentions
  </a>

  <a href="/feed/follow" class="sn-btn" class:active={feedsActive}>
    <span class="sn-icon">📰</span>Feed
  </a>
  <a href="/feed/follow" class="sn-sub" class:active={followSubActive}>
    <span class="sn-dot"></span>Follow
  </a>
  <a href="/feed/broadcast" class="sn-sub" class:active={broadcastSubActive}>
    <span class="sn-dot"></span>Broadcast
  </a>

  <a href="/feed/lists" class="sn-btn" class:active={listsActive}>
    <span class="sn-icon">📋</span>Lists
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

  <a href="/account" class="sn-btn" class:active={accountActive}>
    <span class="sn-icon">👤</span>Account
  </a>
</nav>

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
      width: 220px;
      background: var(--surface);
      border-right: 1px solid var(--border);
      padding: 1rem 0.7rem;
      overflow-y: auto;
      z-index: 90;
    }
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
</style>
