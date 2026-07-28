<script>
  import { createEventDispatcher, onMount } from 'svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import Avatar from '$lib/components/Avatar.svelte';
  import { followingRepository } from '$lib/repositories/following';
  import { apiService } from '$lib/services/api';

  export let user;
  export let editable = false;
  export let isOwner = false;

  const dispatch = createEventDispatcher();

  let following = false;
  let followersCount = 0;
  let followingCount = 0;

  $: if (user) {
    followersCount = user.followersCount ?? 0;
    followingCount = user.followingCount ?? 0;
  }

  onMount(async () => {
    if (!isOwner && user?.id) {
      following = await followingRepository.isFollowing(user.id);
    }
  });

  async function refreshFollowCounts() {
    if (!user?.id) return;
    try {
      const fresh = await apiService.getUser(user.id);
      followersCount = fresh.followersCount ?? 0;
      followingCount = fresh.followingCount ?? 0;
    } catch (error) {
      console.error('Failed to refresh follow counts:', error);
    }
  }

  async function toggleFollow() {
    if (following) {
      await followingRepository.unfollow(user.id);
      following = false;
    } else {
      await followingRepository.follow(user.id);
      following = true;
    }
    await refreshFollowCounts();
  }

  const serverName = localStorage.getItem('serverName') || '';

  function formatDate(dateString) {
    try {
      const date = new Date(dateString);
      return date.toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' });
    } catch {
      return dateString;
    }
  }
</script>

<div class="profile-card">
  <div class="profile-header">
    <div class="avatar-container">
      {#if user?.id}
        <Avatar userID={user.id} username={user.username} size="80px" />
      {/if}
    </div>
    <div class="profile-info">
      <h2>{user?.username}</h2>
      <div class="user-id-container">
        <p class="user-info">{serverName}</p>
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

  .user-info {
    margin: 0;
    color: var(--muted);
    font-family: monospace;
    font-size: 0.8rem;
    text-align: left;
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
