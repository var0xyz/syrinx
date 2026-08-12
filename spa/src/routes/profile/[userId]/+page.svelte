<script lang="ts">
  import { onDestroy } from 'svelte';
  import { beforeNavigate, goto, invalidateAll } from '$app/navigation';
  import { page } from '$app/stores';
  import { apiService } from '$lib/services/api';
  import { authService } from '$lib/services/auth';
  import { cryptoService } from '$lib/services/crypto';
  import { buildUserIdentityPayload } from '$lib/services/signing';
  import { serverConnection } from '$lib/services/serverConnection';
  import { userRepository } from '$lib/repositories/user';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { privateKeyRepository } from '$lib/repositories/privateKey';
  import { reedsService, profileReedQueue } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import { notificationStore } from '$lib/stores/notifications';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';
  import UserProfileCard from '$lib/components/UserProfileCard.svelte';
  import UsernameChecker from '$lib/components/UsernameChecker.svelte';
  import { captureWindowScroll } from '$lib/utils/scrollSnapshot';
  import { mergeUserView, profileNeedsRefresh } from '$lib/utils/userView';
  import { countMarkdownCharacters, MAX_REED_VISIBLE_CHARS } from '$lib/utils/reedContent';
  import type * as api from '$lib/types/api';

  /** @type {import('./$types').PageData} */
  export let data;

  $: userId = $page.params.userId;

  // 'loading' | 'tombstone' | 'notFound' | 'noContent' | 'ready' | 'error'
  let status = data.status;
  let isOwner = data.isOwner;
  let isFollowing = data.isFollowing;
  let profileUser = data.profileUser;
  let profileSubscriptionActive = false;
  let tombstoneNote = data.tombstoneNote;
  /** Account removal cert is local — never re-fetch profile from server. */
  let accountRemoved = data.accountRemoved ?? data.status === 'tombstone';

  // Edit mode state (own profile only — see isOwner gate on the button).
  let isEditing = false;
  let editForm = { username: '', bio: '' };
  let editError = '';
  let editSuccess = '';
  let saving = false;
  /** Markdown-stripped bio length, same budget/counting as reed content. */
  $: visibleBioChars = countMarkdownCharacters(editForm.bio);

  function startEditing() {
    if (!profileUser) return;
    isEditing = true;
    editForm = {
      username: profileUser.username || '',
      bio: profileUser.bio || ''
    };
    editError = '';
    editSuccess = '';
  }

  function cancelEditing() {
    isEditing = false;
    editForm = { username: '', bio: '' };
    editError = '';
    editSuccess = '';
  }

  async function saveProfile() {
    if (!profileUser) return;
    saving = true;
    editError = '';
    editSuccess = '';

    try {
      // Normalise once so the values we validate, sign, and send are
      // the same bytes. Server verifies against these exact strings —
      // any post-hoc trimming would break signature verification.
      const nextUsername = editForm.username.trim();
      const nextBio = editForm.bio.trim();

      if (nextUsername === '') {
        editError = 'Username is required';
        return;
      }
      if (nextUsername.length > 32) {
        editError = 'Username cannot exceed 32 characters';
        return;
      }
      if (visibleBioChars > MAX_REED_VISIBLE_CHARS) {
        editError = `Bio cannot exceed ${MAX_REED_VISIBLE_CHARS} characters`;
        return;
      }

      // Skip the network entirely when nothing changed.
      const unchanged =
        nextUsername === profileUser.username &&
        nextBio === (profileUser.bio || '');
      if (unchanged) {
        isEditing = false;
        return;
      }

      // Build and sign the identity-user payload. Bytes MUST match
      // what the server rebuilds via buildUserIdentityPayload in
      // identity.go — see signing.ts for the mirror contract. The
      // signature travels as base64(armored PGP) to survive
      // form-encoding.
      const fingerprint = authService.getActiveKeyFingerprint();
      const passphrase = authService.getPassphrase();
      if (!fingerprint || !passphrase) {
        editError = 'Session expired. Please sign in again.';
        return;
      }
      const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
      if (!privateKey) {
        editError = 'Could not locate your signing key.';
        return;
      }
      const payload = buildUserIdentityPayload(nextUsername, fingerprint, nextBio);
      const sigArmor = await cryptoService.signMessage(payload, privateKey.armor, passphrase);
      const userSignature = btoa(sigArmor);

      const updatedUser = await apiService.updateUser({
        username: nextUsername,
        bio: nextBio,
        userSignature,
      });

      await authService.saveUserToStorage(updatedUser);
      const cachedInfo = await userInfoRepository.get(updatedUser.id);
      profileUser = mergeUserView(updatedUser, cachedInfo);
      // Root layout's `currentUser` (parent load) is otherwise cached across
      // client-side navigations and would repaint the old username for an
      // instant next time this page loads — force it to re-run now, while
      // the save is fresh, not later when the stale value would flash first.
      await invalidateAll();
      editSuccess = 'Profile updated successfully!';

      setTimeout(() => {
        editSuccess = '';
      }, 3000);

      isEditing = false;
    } catch (error) {
      console.error('Error updating profile:', error);
      editError = error instanceof Error ? error.message : 'Failed to update profile. Please try again.';
    } finally {
      saving = false;
    }
  }
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

  // Follow-list modal state lives in the URL hash (#following / #followers),
  // not component state — this page's [userId] segment is reused (not
  // remounted) across same-route param changes, so plain `let` state or
  // even the snapshot above does not reliably survive "open modal, click a
  // row, navigate to another /profile/[userId], hit back." The hash is
  // part of the history entry itself, so back/forward always restores it
  // correctly, matching the working pattern in routes/feeds/+page.svelte.
  /** @param {string} hash */
  function followListModeFromHash(hash) {
    const h = (hash || '').replace(/^#/, '').toLowerCase();
    return h === 'following' || h === 'followers' ? h : null;
  }

  $: followListMode = followListModeFromHash($page.url.hash) ?? 'following';
  $: followListOpen = followListModeFromHash($page.url.hash) !== null;

  /** @param {boolean} open @param {string} mode */
  function setFollowListHash(open, mode) {
    const hash = open ? `#${mode}` : '';
    void goto(`/profile/${userId}${hash}`, {
      replaceState: !open,
      noScroll: true,
      keepFocus: true,
    });
  }

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
    try {
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
    } catch (error) {
      if (seq !== infoFetchSeq) return;
      console.error('Failed to load profile:', error);
      notificationStore.error(
        error instanceof Error && error.message.includes('verification failed')
          ? 'This profile could not be verified and was rejected for your safety.'
          : 'Failed to load this profile. Please try again.'
      );
      status = 'error';
    }
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
          {#if isEditing && isOwner}
            <div class="profile-card">
              <div class="edit-form">
                <div class="form-group">
                  <label for="edit-username">Username</label>
                  <input
                    id="edit-username"
                    type="text"
                    bind:value={editForm.username}
                    placeholder="Enter username"
                    maxlength="50"
                  />
                  {#if editForm.username && editForm.username !== profileUser.username}
                    <div class="help-text">
                      <UsernameChecker username={editForm.username} authenticated />
                    </div>
                  {/if}
                </div>

                <div class="form-group">
                  <label for="edit-bio">Bio</label>
                  <textarea
                    id="edit-bio"
                    bind:value={editForm.bio}
                    placeholder="Tell us about yourself..."
                    rows="3"
                  ></textarea>
                  <div class="char-count" class:over-limit={visibleBioChars > MAX_REED_VISIBLE_CHARS}>{visibleBioChars}/{MAX_REED_VISIBLE_CHARS}</div>
                </div>

                {#if editError}
                  <div class="error-message">
                    <p>{editError}</p>
                  </div>
                {/if}

                {#if editSuccess}
                  <div class="success-message">
                    <p>{editSuccess}</p>
                  </div>
                {/if}

                <div class="edit-actions">
                  <button class="action-btn secondary" on:click={cancelEditing} disabled={saving}>
                    Cancel
                  </button>
                  <button class="action-btn primary" on:click={saveProfile} disabled={saving}>
                    {saving ? 'Saving...' : 'Save Changes'}
                  </button>
                </div>
              </div>
            </div>
          {:else}
            <UserProfileCard
              user={profileUser}
              {isOwner}
              {isFollowing}
              {followListOpen}
              {followListMode}
              on:edit={startEditing}
              on:followingChange={onFollowingChange}
              on:openFollowList={(e) => setFollowListHash(true, e.detail.mode)}
              on:closeFollowList={() => setFollowListHash(false, followListMode)}
            />
          {/if}
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

    {:else if status === 'error'}
      <div class="state-message">
        <div class="state-icon">⚠️</div>
        <h3>Couldn't load this profile</h3>
        <p>Something went wrong loading this profile. Please try again.</p>
      </div>

    {:else if status === 'noContent'}
      {#if profileUser}
        <div class="user-profile-card-container">
          {#if isEditing && isOwner}
            <div class="profile-card">
              <div class="edit-form">
                <div class="form-group">
                  <label for="edit-username">Username</label>
                  <input
                    id="edit-username"
                    type="text"
                    bind:value={editForm.username}
                    placeholder="Enter username"
                    maxlength="50"
                  />
                  {#if editForm.username && editForm.username !== profileUser.username}
                    <div class="help-text">
                      <UsernameChecker username={editForm.username} authenticated />
                    </div>
                  {/if}
                </div>

                <div class="form-group">
                  <label for="edit-bio">Bio</label>
                  <textarea
                    id="edit-bio"
                    bind:value={editForm.bio}
                    placeholder="Tell us about yourself..."
                    rows="3"
                  ></textarea>
                  <div class="char-count" class:over-limit={visibleBioChars > MAX_REED_VISIBLE_CHARS}>{visibleBioChars}/{MAX_REED_VISIBLE_CHARS}</div>
                </div>

                {#if editError}
                  <div class="error-message">
                    <p>{editError}</p>
                  </div>
                {/if}

                {#if editSuccess}
                  <div class="success-message">
                    <p>{editSuccess}</p>
                  </div>
                {/if}

                <div class="edit-actions">
                  <button class="action-btn secondary" on:click={cancelEditing} disabled={saving}>
                    Cancel
                  </button>
                  <button class="action-btn primary" on:click={saveProfile} disabled={saving}>
                    {saving ? 'Saving...' : 'Save Changes'}
                  </button>
                </div>
              </div>
            </div>
          {:else}
            <UserProfileCard
              user={profileUser}
              {isOwner}
              {isFollowing}
              {followListOpen}
              {followListMode}
              on:edit={startEditing}
              on:followingChange={onFollowingChange}
              on:openFollowList={(e) => setFollowListHash(true, e.detail.mode)}
              on:closeFollowList={() => setFollowListHash(false, followListMode)}
            />
          {/if}
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
          {#if isEditing && isOwner}
            <div class="profile-card">
              <div class="edit-form">
                <div class="form-group">
                  <label for="edit-username">Username</label>
                  <input
                    id="edit-username"
                    type="text"
                    bind:value={editForm.username}
                    placeholder="Enter username"
                    maxlength="50"
                  />
                  {#if editForm.username && editForm.username !== profileUser.username}
                    <div class="help-text">
                      <UsernameChecker username={editForm.username} authenticated />
                    </div>
                  {/if}
                </div>

                <div class="form-group">
                  <label for="edit-bio">Bio</label>
                  <textarea
                    id="edit-bio"
                    bind:value={editForm.bio}
                    placeholder="Tell us about yourself..."
                    rows="3"
                  ></textarea>
                  <div class="char-count" class:over-limit={visibleBioChars > MAX_REED_VISIBLE_CHARS}>{visibleBioChars}/{MAX_REED_VISIBLE_CHARS}</div>
                </div>

                {#if editError}
                  <div class="error-message">
                    <p>{editError}</p>
                  </div>
                {/if}

                {#if editSuccess}
                  <div class="success-message">
                    <p>{editSuccess}</p>
                  </div>
                {/if}

                <div class="edit-actions">
                  <button class="action-btn secondary" on:click={cancelEditing} disabled={saving}>
                    Cancel
                  </button>
                  <button class="action-btn primary" on:click={saveProfile} disabled={saving}>
                    {saving ? 'Saving...' : 'Save Changes'}
                  </button>
                </div>
              </div>
            </div>
          {:else}
            <UserProfileCard
              user={profileUser}
              {isOwner}
              {isFollowing}
              {followListOpen}
              {followListMode}
              on:edit={startEditing}
              on:followingChange={onFollowingChange}
              on:openFollowList={(e) => setFollowListHash(true, e.detail.mode)}
              on:closeFollowList={() => setFollowListHash(false, followListMode)}
            />
          {/if}
        </div>
      {/if}
      {#key profileUser?.username}
        <ReedsList
          authorId={userId}
          {isOwner}
          showWriteButton={isOwner}
          {scrollRestoreY}
          expectContent={!isOwner}
        />
      {/key}
    {/if}
  </div>

  <BottomToolbar currentPage="reeds" />
</div>

<style>
  .profile-container {
    min-height: calc(100vh - 3rem - 1px);
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

  .profile-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    text-align: center;
  }

  .edit-form {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    margin: 0.75rem;
  }

  .form-group {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .form-group label {
    color: var(--fg);
    font-weight: 600;
    font-size: 0.9rem;
  }

  .form-group input,
  .form-group textarea {
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
  }

  .form-group input:focus,
  .form-group textarea:focus {
    outline: none;
    border-color: var(--primary);
  }

  .form-group textarea {
    resize: vertical;
    min-height: 80px;
  }

  .char-count {
    color: var(--muted);
    font-size: 0.8rem;
    text-align: right;
  }

  .char-count.over-limit {
    color: var(--error);
    font-weight: 600;
  }

  .help-text {
    color: var(--muted);
    font-size: 0.8rem;
    text-align: left;
  }

  .edit-actions {
    display: flex;
    gap: 0.75rem;
    justify-content: center;
  }

  .success-message {
    background: rgba(76, 175, 80, 0.1);
    border: 1px solid rgba(76, 175, 80, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    margin: 1rem 0;
  }

  .success-message p {
    margin: 0;
    color: #4caf50;
    font-size: 0.9rem;
  }

  .error-message {
    background: rgba(244, 67, 54, 0.1);
    border: 1px solid rgba(244, 67, 54, 0.3);
    border-radius: 6px;
    padding: 0.75rem;
    margin: 1rem 0;
  }

  .error-message p {
    margin: 0;
    color: var(--error);
    font-size: 0.9rem;
  }

  .action-btn {
    padding: 0.75rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    border: 1px solid var(--border);
  }

  .action-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .action-btn.primary {
    background: var(--primary);
    color: var(--button-text);
    border-color: var(--primary);
  }

  .action-btn.secondary {
    background: var(--surface);
    color: var(--fg);
    border-color: var(--border);
  }

  .action-btn.secondary:hover {
    background: var(--input-bg);
    border-color: var(--primary);
  }

  .action-btn:hover {
    opacity: 0.9;
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
