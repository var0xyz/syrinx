<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { authService } from '$lib/services/auth';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';
  import { reedsService } from '$lib/repositories/reeds';
  import { publicKeyRepository } from '$lib/repositories/publicKey';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';
  import UserProfileCard from '$lib/components/UserProfileCard.svelte';

  $: userId = $page.params.userId;

  // 'loading' | 'tombstone' | 'notFound' | 'noContent' | 'ready'
  let status = 'loading';
  let isOwner = false;
  let profileUser = null;

  onMount(async () => {
    const currentUser = await authService.getCurrentUser().catch(() => null);
    isOwner = currentUser?.id === userId;

    // Tombstone check — runs first, always
    if (await userRepository.isTombstone(userId)) {
      status = 'tombstone';
      return;
    }

    const localReeds = await reedsService.getReedsByAuthor(userId);

    if (localReeds.length > 0) {
      // Case 1 — render immediately, refresh user in background
      profileUser = isOwner ? currentUser : await userRepository.getByUserId(userId).catch(() => null);
      status = 'ready';
      refreshUserInBackground();
    } else {
      // Case 2 — no local data, ask the server
      const { status: httpStatus, user } = await apiService.getUserWithStatus(userId);

      if (httpStatus === 404) {
        status = 'notFound';
      } else if (httpStatus === 410) {
        await handleGone();
      } else if (httpStatus === 200) {
        await userRepository.put(user);
        profileUser = user;

        if (user.hasReeds) {
          await fetchAndRequestReeds(userId);
          status = 'ready';
        } else {
          status = 'noContent';
        }
      }
    }
  });

  async function refreshUserInBackground() {
    try {
      const { status: httpStatus, user } = await apiService.getUserWithStatus(userId);

      if (httpStatus === 200) {
        await userRepository.put(user);
        profileUser = user;
      } else if (httpStatus === 410) {
        await handleGone();
      }
    } catch (error) {
      console.error('Background user refresh failed:', error);
    }
  }

  async function fetchAndRequestReeds(uid) {
    // Paginate through all reed IDs and store in reedQueue
    let cursor;
    do {
      const ids = await apiService.getUserReedIds(uid, cursor);
      await Promise.all(ids.map(id => dbService.put('reedQueue', { id })));
      cursor = ids.length === 100 ? ids[ids.length - 1] : undefined;
    } while (cursor);

    // Fire relay requests for all queued IDs in parallel
    const queued = await dbService.getAllSortedByIndex<{ id: string }>('reedQueue', '__meta__.created');
    await Promise.all(queued.map(async ({ id: reedId }) => {
      const data = await serverConnection.requestReedContent(reedId);
      await reedsService.storeReed(data);
      await dbService.delete('reedQueue', reedId);
    }));
  }

  async function handleGone() {
    await reedsService.deleteReedsByAuthor(userId);

    // Delete associated public keys
    const userRecord = await userRepository.get(userId);
    if (userRecord?.publicKeys?.length) {
      await Promise.all(userRecord.publicKeys.map(fp => publicKeyRepository.deletePublicKey(fp)));
    }

    await userRepository.writeTombstone(userId);
    profileUser = null;
    status = 'tombstone';
  }
</script>

<div class="profile-container">
  <div class="profile-content">
    {#if status === 'loading'}
      <div class="state-message">
        <p>Loading...</p>
      </div>

    {:else if status === 'tombstone'}
      <div class="state-message">
        <div class="state-icon">🪦</div>
        <h3>Account deleted</h3>
        <p>This account no longer exists.</p>
      </div>

    {:else if status === 'notFound'}
      <div class="state-message">
        <div class="state-icon">🔍</div>
        <h3>User not found</h3>
        <p>No account exists with this ID.</p>
      </div>

    {:else if status === 'noContent'}
      {#if profileUser}
        <UserProfileCard user={profileUser} {isOwner} />
      {/if}
      <div class="state-message">
        <div class="state-icon">🌱</div>
        <h3>No reeds yet</h3>
        <p>New reeds will appear here once we receive them.</p>
      </div>

    {:else if status === 'ready'}
      {#if profileUser}
        <UserProfileCard user={profileUser} {isOwner} />
      {/if}
      <ReedsList authorId={userId} {isOwner} showWriteButton={false} />
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

  @media (max-width: 768px) {
    .profile-content {
      padding: 0.5rem;
    }
  }
</style>
