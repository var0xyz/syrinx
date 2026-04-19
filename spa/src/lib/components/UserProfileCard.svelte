<script>
  import { createEventDispatcher, onMount } from 'svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { followingRepository } from '$lib/repositories/following';

  export let user;
  export let editable = false;
  export let isOwner = false;

  const dispatch = createEventDispatcher();

  let following = false;

  onMount(async () => {
    if (!isOwner && user?.id) {
      following = await followingRepository.isFollowing(user.id);
    }
  });

  async function toggleFollow() {
    if (following) {
      await followingRepository.unfollow(user.id);
      following = false;
    } else {
      await followingRepository.follow(user.id);
      following = true;
    }
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

  async function copyUserId() {
    if (!user?.id) return;
    try {
      await navigator.clipboard.writeText(serverName ? `${user.id}@${serverName}` : user.id);
      notificationStore.success('User ID copied to clipboard');
    } catch (error) {
      console.error('Failed to copy user ID:', error);
      notificationStore.error('Failed to copy user ID');
    }
  }
</script>

<div class="profile-card">
  <div class="profile-header">
    <div class="avatar-container">
      {#if user?.avatarURL}
        <img src={user.avatarURL} alt="{user.username}'s Avatar" class="profile-avatar" />
      {:else}
        <div class="profile-avatar-icon">👤</div>
      {/if}
    </div>
    <div class="profile-info">
      <h2>{user?.username}</h2>
      <div class="user-id-container">
        <p class="user-id">{user?.id}{serverName ? `@${serverName}` : ''}</p>
        <CopyButton ariaLabel="Copy user ID" on:click={copyUserId} />
      </div>
      <p class="member-since">{user?.memberSince ? formatDate(user.memberSince) : 'Unknown'}</p>
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

  .profile-avatar {
    width: 80px;
    max-width: 80px;
    height: 80px;
    min-height: 80px;
    border-radius: 50%;
    object-fit: cover;
    border: 2px solid var(--border);
  }

  .profile-avatar-icon {
    width: 80px;
    height: 80px;
    border-radius: 50%;
    background: var(--input-bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 2rem;
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

  .user-id-container {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .user-id {
    margin: 0;
    color: var(--muted);
    font-family: monospace;
    font-size: 0.8rem;
    text-align: left;
  }

  .member-since {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
    text-align: left;
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
