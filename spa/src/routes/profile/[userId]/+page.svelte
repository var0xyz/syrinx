<script lang="ts">
  import { onDestroy } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { page } from '$app/stores';
  import { apiService } from '$lib/services/api';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';
  import { reedsService, profileReedQueue } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';
  import UserProfileCard from '$lib/components/UserProfileCard.svelte';
  import { captureWindowScroll } from '$lib/utils/scrollSnapshot';

  /** @type {import('./$types').PageData} */
  export let data;

  $: userId = $page.params.userId;

  // 'loading' | 'tombstone' | 'notFound' | 'noContent' | 'ready'
  let status = data.status;
  let isOwner = data.isOwner;
  let profileUser = data.profileUser;
  let profileSubscriptionActive = false;
  let tombstoneNote = data.tombstoneNote;

  /** @type {number | null} */
  let scrollRestoreY = null;

  /** @type {import('./$types').Snapshot<number>} */
  export const snapshot = {
    capture: () => captureWindowScroll(),
    restore: (y) => {
      scrollRestoreY = y;
    },
  };

  $: applyPageData(data);

  // Promote out of noContent the moment the subscribed profile's first reed
  // arrives — ReedsList (which normally handles profileReedQueue) isn't
  // mounted while status is noContent, so nothing else would flip this.
  let lastHandledProfileReedId = '';
  $: profileArrived = $profileReedQueue?.reed;
  $: if (
    profileArrived &&
    profileArrived.userID === userId &&
    profileArrived.id !== lastHandledProfileReedId &&
    status === 'noContent'
  ) {
    lastHandledProfileReedId = profileArrived.id;
    status = 'ready';
  }

  function applyPageData(next) {
    status = next.status;
    isOwner = next.isOwner;
    profileUser = next.profileUser;
    tombstoneNote = next.tombstoneNote;
    if (next.fromCache && next.status === 'ready') {
      void refreshUserInBackground();
      void subscribeToProfileIfNotFollowing(next.userId);
    } else if (!next.fromCache && next.status === 'loading') {
      void loadFromNetwork(next.userId);
    }
  }

  async function loadFromNetwork(uid: string) {
    const { status: httpStatus, user, removal } = await apiService.getUserWithStatus(uid);

    if (httpStatus === 404) {
      status = 'notFound';
    } else if (httpStatus === 410) {
      await handleGone(removal);
    } else if (httpStatus === 200) {
      await userRepository.put(user);
      profileUser = user;

      // Subscribe regardless of hasReeds: with zero reeds today, this is the
      // only way to hear about the author's first reed live while we're
      // sitting on this page (ReedsList — which handles profileReedQueue —
      // isn't even mounted in the noContent state below).
      await subscribeToProfileIfNotFollowing(uid);
      status = user.hasReeds ? 'ready' : 'noContent';
    }
  }

  async function refreshUserInBackground() {
    try {
      const { status: httpStatus, user, removal } = await apiService.getUserWithStatus(userId);

      if (httpStatus === 200) {
        await userRepository.put(user);
        profileUser = user;
      } else if (httpStatus === 410) {
        await handleGone(removal);
      }
    } catch (error) {
      console.error('Background user refresh failed:', error);
    }
  }

  async function subscribeToProfileIfNotFollowing(uid: string) {
    if (isOwner) return;
    if (await followingRepository.isFollowing(uid)) return;
    await subscribeToProfile(uid);
  }

  async function subscribeToProfile(uid: string) {
    await serverConnection.subscribeProfile(uid);
    profileSubscriptionActive = true;
  }

  function cleanupProfileSubscription() {
    if (profileSubscriptionActive) {
      serverConnection.unsubscribeProfile(userId);
      profileSubscriptionActive = false;
    }
  }

  onDestroy(() => {
    cleanupProfileSubscription();
  });

  beforeNavigate(() => {
    cleanupProfileSubscription();
  });

  async function handleGone(removal) {
    if (removal?.type === 'account') {
      if (!(await verifyAndCommitAccountRemoval(removal))) {
        console.warn('Account removal cert failed verification; retaining local data');
        return;
      }
      tombstoneNote = removal.note ?? '';
    } else {
      await reedsService.deleteReedsByAuthor(userId);
      await userRepository.writeTombstone(userId);
      tombstoneNote = '';
    }
    profileUser = null;
    status = 'tombstone';
  }
</script>

<div class="profile-container">
  <div class="profile-content">
    {#if status === 'loading'}
      {#if profileUser}
        <div class="user-profile-card-container">
          <UserProfileCard user={profileUser} {isOwner} />
        </div>
      {/if}
      <div class="state-message">
        <div class="state-icon">🌱</div>
        <h3>Loading...</h3>
        <p>New reeds will appear here once we receive them.</p>
      </div>

    {:else if status === 'tombstone'}
      <div class="state-message">
        <div class="state-icon">🪦</div>
        <h3>Account deleted</h3>
        {#if tombstoneNote}
          <p class="tombstone-note">{tombstoneNote}</p>
        {:else}
          <p>This account no longer exists.</p>
        {/if}
      </div>

    {:else if status === 'notFound'}
      <div class="state-message">
        <div class="state-icon">🔍</div>
        <h3>User not found</h3>
        <p>No account exists with this ID.</p>
      </div>

    {:else if status === 'noContent'}
      {#if profileUser}
        <div class="user-profile-card-container">
          <UserProfileCard user={profileUser} {isOwner} />
        </div>
      {/if}
      <div class="state-message">
        <div class="state-icon">🫙</div>
        <h3>No reeds yet</h3>
        <p>New reeds will appear here once we receive them.</p>
      </div>

    {:else if status === 'ready'}
      {#if profileUser}
        <div class="user-profile-card-container">
          <UserProfileCard user={profileUser} {isOwner} />
        </div>
      {/if}
      <ReedsList authorId={userId} {isOwner} showWriteButton={false} {scrollRestoreY} />
    {/if}
  </div>

  <BottomToolbar currentPage="reeds" />
</div>

<style>
  .profile-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .profile-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .user-profile-card-container {
    margin-bottom: 2rem;
  }

  .state-message {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .state-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .state-message h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .state-message p {
    margin: 0;
    font-size: 0.9rem;
  }

  .tombstone-note {
    margin-top: 1rem !important;
    font-style: italic;
    color: var(--fg);
  }

  @media (max-width: 768px) {
    .profile-content {
      padding: 0.5rem;
    }

    .user-profile-card-container {
      margin-bottom: 1rem;
    }
  }
</style>
