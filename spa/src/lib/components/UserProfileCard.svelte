<script>
  import { createEventDispatcher } from 'svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import { followingRepository } from '$lib/repositories/following';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { apiService } from '$lib/services/api';

  export let user;
  export let editable = false;
  export let isOwner = false;
  /** Resolved before first paint by the profile page load — avoids Follow→Unfollow flash. */
  export let isFollowing = false;

  const dispatch = createEventDispatcher();

  let following = isFollowing;
  let followersCount = 0;
  let followingCount = 0;

  // Keep local counts aligned with the parent merge. Stale /info overwrites are
  // prevented on the profile page (fetch seq invalidated on follow).
  $: userCountsKey = `${user?.id ?? ''}:${user?.followersCount ?? ''}:${user?.followingCount ?? ''}`;
  $: if (userCountsKey) {
    followersCount = user?.followersCount ?? 0;
    followingCount = user?.followingCount ?? 0;
  }

  // Re-sync follow button when the profile identity or server-known follow
  // state changes (e.g. client-side nav). Do not depend on `following`
  // itself or a local Follow/Unfollow would be overwritten by a stale prop.
  $: followSyncKey = `${user?.id ?? ''}:${isFollowing}`;
  $: if (followSyncKey) {
    following = isFollowing;
  }

  function emitFollowState() {
    dispatch('followingChange', {
      following,
      followersCount,
      followingCount,
    });
  }

  /** Keep the viewer's own cached followingCount in step with local follows. */
  async function bumpOwnFollowingCount(delta) {
    const myId = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
    if (!myId || myId === user?.id) return;
    try {
      const mine = await userInfoRepository.get(myId);
      if (!mine) return;
      await userInfoRepository.put({
        ...mine,
        followingCount: Math.max(0, (mine.followingCount ?? 0) + delta),
      });
    } catch (error) {
      console.error('Failed to bump own followingCount cache:', error);
    }
  }

  async function refreshFollowCounts() {
    if (!user?.id) return;
    try {
      const fresh = await apiService.getUserInfo(user.id);
      await userInfoRepository.put(fresh);
      followersCount = fresh.followersCount ?? 0;
      followingCount = fresh.followingCount ?? 0;
      emitFollowState();
    } catch (error) {
      console.error('Failed to refresh follow counts:', error);
    }
  }

  async function toggleFollow() {
    if (following) {
      following = false;
      followersCount = Math.max(0, followersCount - 1);
      emitFollowState();
      await bumpOwnFollowingCount(-1);
      await followingRepository.unfollow(user.id);
    } else {
      following = true;
      followersCount = followersCount + 1;
      emitFollowState();
      await bumpOwnFollowingCount(1);
      await followingRepository.follow(user.id);
    }
    await refreshFollowCounts();
  }

  const serverName = localStorage.getItem('serverName') || '';
  $: serverId = user?.serverSignature?.serverID || localStorage.getItem('serverId') || '';
  $: serverLabel =
    serverId && serverName
      ? `${serverName} (${serverId})`
      : serverId || serverName;

  let avatarOpen = false;

  function openAvatar() {
    if (!user?.id) return;
    avatarOpen = true;
  }

  function closeAvatar() {
    avatarOpen = false;
  }

  function onWindowKeydown(event) {
    if (event.key === 'Escape' && avatarOpen) closeAvatar();
  }

  function formatDate(dateString) {
    try {
      const date = new Date(dateString);
      return date.toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' });
    } catch {
      return dateString;
    }
  }
</script>

<svelte:window on:keydown={onWindowKeydown} />

<div class="profile-card">
  <div class="profile-header">
    <div class="avatar-container">
      {#if user?.id}
        <button
          type="button"
          class="avatar-button"
          on:click={openAvatar}
          aria-label={user.username ? `View ${user.username}'s avatar` : 'View avatar'}
        >
          <Avatar userID={user.id} username={user.username} size="80px" />
        </button>
      {/if}
    </div>
    <div class="profile-info">
      <h2 class:root-username={user?.id === '1'}>{user?.username}</h2>
      <div class="user-id-container">
        {#if serverLabel}
          <p class="user-info">{serverLabel}</p>
        {/if}
        <p class="user-info">{user?.id}</p>
      </div>
      <p class="user-info">{user?.memberSince ? formatDate(user.memberSince) : 'Unknown'}</p>
      {#if user?.invitedBy}
        <p class="user-info invited-by">
          Invited by
          <a href="/profile/{user.invitedBy.id}">@{user.invitedBy.username}</a>
        </p>
      {/if}
      <div class="follow-stats">
        <span class="follow-stat"><strong>{followingCount}</strong> Following</span>
        <span class="follow-stat"><strong>{followersCount}</strong> Followers</span>
      </div>
    </div>
  </div>
  {#if user?.bio}
    <div class="user-bio">
      <MarkdownParser text={user.bio} />
    </div>
  {/if}
  {#if editable}
    <div class="profile-actions">
      <button class="action-btn primary" on:click={() => dispatch('edit')}>Edit Profile</button>
    </div>
  {:else if !isOwner}
    <div class="profile-actions">
      <button class="action-btn" class:primary={!following} class:secondary={following} on:click={toggleFollow}>
        {following ? 'Unfollow' : 'Follow'}
      </button>
    </div>
  {/if}
</div>

{#if avatarOpen && user?.id}
  <div
    class="avatar-overlay"
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    aria-label={user.username ? `${user.username}'s avatar` : 'Avatar'}
    on:click={(e) => e.target === e.currentTarget && closeAvatar()}
    on:keydown={(e) => (e.key === 'Enter' || e.key === 'Escape') && closeAvatar()}
  >
    <div class="avatar-lightbox">
      <Avatar userID={user.id} username={user.username} size="100%" />
    </div>
  </div>
{/if}

<style>
  .profile-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem;
    margin-bottom: 1rem;
  }

  .profile-header {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }

  .avatar-container {
    flex-shrink: 0;
  }

  .avatar-button {
    display: block;
    padding: 0;
    margin: 0;
    border: none;
    background: transparent;
    cursor: zoom-in;
    border-radius: 8px;
    line-height: 0;
  }

  .avatar-button:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
  }

  .avatar-overlay {
    position: fixed;
    inset: 0;
    z-index: 2000;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.5rem;
    background: rgba(0, 0, 0, 0.72);
    cursor: zoom-out;
  }

  .avatar-lightbox {
    width: min(80vw, 80vh, 28rem);
    height: min(80vw, 80vh, 28rem);
    cursor: default;
    filter: drop-shadow(0 8px 32px rgba(0, 0, 0, 0.45));
  }

  .profile-info {
    flex: 1;
    min-width: 0;
  }

  .profile-info h2 {
    text-align: left;
    margin: 0;
    color: var(--fg);
    word-break: break-word;
  }

  .profile-info h2.root-username {
    font-style: italic;
  }

  .user-id-container {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.15rem;
    max-width: 100%;
    min-width: 0;
  }

  .user-info {
    margin: 0;
    color: var(--muted);
    font-family: monospace;
    font-size: 0.8rem;
    text-align: left;
    max-width: 100%;
    overflow-wrap: anywhere;
    word-break: break-word;
  }

  .invited-by a {
    color: var(--primary);
    text-decoration: none;
  }

  .invited-by a:hover {
    text-decoration: underline;
  }

  .follow-stats {
    display: flex;
    gap: 1rem;
    margin-top: 0.5rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .follow-stat strong {
    color: var(--fg);
    font-weight: 600;
  }

  .user-bio {
    text-align: left;
    font-size: 0.9rem;
    line-height: 1.4;
    margin-top: 0.5rem;
  }

  .profile-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 1rem;
  }

  .action-btn {
    padding: 0.5rem 1rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    transition: all 0.2s ease;
  }

  .action-btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .action-btn.primary:hover {
    opacity: 0.9;
  }

  .action-btn.secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .action-btn.secondary:hover {
    background: var(--border);
  }

  @media (max-width: 768px) {
    .profile-actions {
      flex-direction: column;
      gap: 0.5rem;
    }
  }

  @media (max-width: 768px) {
    .profile-card {
      padding: 0.75rem;
      margin-bottom: 0.5rem;
    }
  }
</style>
