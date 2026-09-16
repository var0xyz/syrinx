<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import { dbService } from '$lib/services/db';
  import { userRepository } from '$lib/repositories/user';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { mergeUserView } from '$lib/utils/userView';
  import { listsRepository, LIST_NAME_MAX, LIST_DESCRIPTION_MAX } from '$lib/repositories/lists';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import type { ListType } from '$lib/types/list';

  export let list: ListType | null = null;

  const dispatch = createEventDispatcher();

  let name = list?.name ?? '';
  let description = list?.description ?? '';
  let selectedMemberIds = new Set(list?.memberIds ?? []);
  let followingUsers: Array<{ userId: string; username: string }> = [];
  let loadingFollowing = true;
  let saveError = '';
  let saving = false;

  $: nameCount = name.length;
  $: descriptionCount = description.length;

  onMount(async () => {
    const following = await dbService.getAll<{ userId: string }>('following');
    followingUsers = await Promise.all(
      following.map(async (f) => {
        const [profile, info] = await Promise.all([
          userRepository.get(f.userId),
          userInfoRepository.get(f.userId),
        ]);
        const merged = mergeUserView(profile, info);
        return { userId: f.userId, username: merged?.username ?? f.userId };
      })
    );
    loadingFollowing = false;
  });

  function toggleMember(userId: string) {
    const next = new Set(selectedMemberIds);
    if (next.has(userId)) {
      next.delete(userId);
    } else {
      next.add(userId);
    }
    selectedMemberIds = next;
  }

  async function save() {
    saving = true;
    saveError = '';
    try {
      const input = { name, description, memberIds: [...selectedMemberIds] };
      if (list) {
        await listsRepository.update(list.id, input);
      } else {
        await listsRepository.create(input);
      }
      dispatch('saved');
    } catch (e) {
      saveError = e instanceof Error ? e.message : 'Failed to save list';
    } finally {
      saving = false;
    }
  }

  function cancel() {
    if (saving) return;
    dispatch('cancel');
  }
</script>

<div class="overlay" on:click={cancel} role="presentation"></div>
<div class="modal" role="dialog" aria-modal="true" aria-labelledby="list-form-title">
  <h3 id="list-form-title">{list ? 'Edit list' : 'New list'}</h3>

  <div class="field">
    <label for="list-name">Name</label>
    <input id="list-name" type="text" bind:value={name} maxlength={LIST_NAME_MAX} placeholder="List name" />
    <div class="char-count" class:over-limit={nameCount > LIST_NAME_MAX}>{nameCount}/{LIST_NAME_MAX}</div>
  </div>

  <div class="field">
    <label for="list-description">Description</label>
    <textarea id="list-description" bind:value={description} maxlength={LIST_DESCRIPTION_MAX} rows="2" placeholder="Optional description"></textarea>
    <div class="char-count" class:over-limit={descriptionCount > LIST_DESCRIPTION_MAX}>{descriptionCount}/{LIST_DESCRIPTION_MAX}</div>
  </div>

  <div class="field">
    <span class="field-label">Members</span>
    <div class="member-picker">
      {#if loadingFollowing}
        <p class="state-text">Loading…</p>
      {:else if followingUsers.length === 0}
        <p class="state-text">You aren't following anyone yet.</p>
      {:else}
        {#each followingUsers as u (u.userId)}
          <div
            class="user-row"
            role="button"
            tabindex="0"
            on:click={() => toggleMember(u.userId)}
            on:keydown={(e) => e.key === 'Enter' && toggleMember(u.userId)}
          >
            <input type="checkbox" checked={selectedMemberIds.has(u.userId)} tabindex="-1" readonly />
            <ReedAuthorHeader userID={u.userId} username={u.username} linked={false} stopPropagation />
          </div>
        {/each}
      {/if}
    </div>
  </div>

  {#if saveError}
    <div class="error-message">
      <p>{saveError}</p>
    </div>
  {/if}

  <div class="actions">
    <button class="btn btn-secondary" on:click={cancel} disabled={saving}>Cancel</button>
    <button class="btn btn-primary" on:click={save} disabled={saving}>
      {saving ? 'Saving...' : 'Save'}
    </button>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 2000;
  }

  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 2001;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    width: min(420px, calc(100vw - 2rem));
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    overflow-y: auto;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .field label,
  .field-label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--fg);
  }

  .field input[type='text'],
  .field textarea {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.9rem;
    font-family: inherit;
  }

  .field textarea {
    resize: vertical;
  }

  .field input[type='text']:focus,
  .field textarea:focus {
    outline: none;
    border-color: var(--primary);
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

  .member-picker {
    max-height: 220px;
    overflow-x: hidden;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.25rem;
  }

  .user-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem;
    border-radius: 8px;
    cursor: pointer;
    min-width: 0;
    overflow: hidden;
  }

  .user-row :global(.reed-author-header) {
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }

  .user-row:hover {
    background: var(--input-bg);
  }

  .user-row input[type='checkbox'] {
    flex-shrink: 0;
    width: 1rem;
    height: 1rem;
    margin: 0;
    pointer-events: none;
  }

  .state-text {
    color: var(--muted);
    text-align: center;
    padding: 1rem 0;
    margin: 0;
    font-size: 0.9rem;
  }

  .error-message p {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
    transition: all 0.2s ease;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:not(:disabled):hover {
    opacity: 0.9;
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--border);
  }
</style>
