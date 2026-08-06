<script lang="ts">
  import { onDestroy } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { page } from '$app/stores';
  import { apiService } from '$lib/services/api';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { reedsService, profileReedQueue } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';
  import UserProfileCard from '$lib/components/UserProfileCard.svelte';
  import { captureWindowScroll } from '$lib/utils/scrollSnapshot';
  import { mergeUserView, profileNeedsRefresh } from '$lib/utils/userView';
  import type * as api from '$lib/types/api';

  /** @type {import('./$types').PageData} */
  export let data;

  $: userId = $page.params.userId;

  // 'loading' | 'tombstone' | 'notFound' | 'noContent' | 'ready'
  let status = data.status;
  let isOwner = data.isOwner;
  let isFollowing = data.isFollowing;
  let profileUser = data.profileUser;
  let profileSubscriptionActive = false;
  let tombstoneNote = data.tombstoneNote;
  /** Account removal cert is local — never re-fetch profile from server. */
  let accountRemoved = data.accountRemoved ?? data.status === 'tombstone';
  /** Ignore stale /info responses that started before a newer refresh. */
  let infoFetchSeq = 0;

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
    isFollowing = next.isFollowing;
    profileUser = next.profileUser;
    tombstoneNote = next.tombstoneNote;
    accountRemoved = next.accountRemoved ?? next.status === 'tombstone';
    if (accountRemoved || next.status === 'tombstone') {
      return;
    }
    if (next.fromCache && (next.status === 'ready' || next.status === 'noContent')) {
      void refreshFromNetwork(next.userId);
      void subscribeToProfileIfNotFollowing(next.userId);
    } else if (!next.fromCache && next.status === 'loading') {
      void refreshFromNetwork(next.userId);
    }
  }

  async function refreshFromNetwork(uid: string) {
    if (accountRemoved || status === 'tombstone') return;
    const seq = ++infoFetchSeq;
    const { status: httpStatus, info, removal } = await apiService.getUserInfoWithStatus(uid);
    if (seq !== infoFetchSeq) return;

    if (httpStatus === 404) {
      status = 'notFound';
      return;
    }
    if (httpStatus === 410) {
      await handleGone(removal);
      return;
    }
    if (httpStatus !== 200 || !info) {
      return;
    }

    await userInfoRepository.put(info);
    if (seq !== infoFetchSeq) return;

    let profile: api.User | null = isOwner
      ? data.currentUser
      : await userRepository.get(uid).catch(() => null);

    if (profileNeedsRefresh(profile, info)) {
      const {
        status: profileStatus,
        user,
        removal: profileRemoval,
      } = await apiService.getUserProfileWithStatus(uid);
      if (seq !== infoFetchSeq) return;
      if (profileStatus === 410) {
        await handleGone(profileRemoval);
        return;
      }
      if (profileStatus === 404) {
        status = 'notFound';
        return;
      }
      if (profileStatus === 200 && user) {
        await userRepository.put(user);
        profile = user;
      }
    }

    if (seq !== infoFetchSeq) return;
    profileUser = mergeUserView(profile, info);
    await subscribeToProfileIfNotFollowing(uid);
    status = info.hasReeds ? 'ready' : 'noContent';
  }

  function onFollowingChange(e) {
    // Drop any in-flight profile /info fetch that started before this follow.
    infoFetchSeq += 1;
    isFollowing = e.detail.following;
    if (profileUser) {
      profileUser = {
        ...profileUser,
        followersCount: e.detail.followersCount ?? profileUser.followersCount,
        followingCount: e.detail.followingCount ?? profileUser.followingCount,
      };
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
      accountRemoved = true;
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
          <UserProfileCard
            user={profileUser}
            {isOwner}
            {isFollowing}
            on:followingChange={onFollowingChange}
          />
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
          <UserProfileCard
            user={profileUser}
            {isOwner}
            {isFollowing}
            on:followingChange={onFollowingChange}
          />
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
          <UserProfileCard
            user={profileUser}
            {isOwner}
            {isFollowing}
            on:followingChange={onFollowingChange}
          />
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
