<script lang="ts">
  import { setContext } from 'svelte';
  import { writable } from 'svelte/store';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import UserListFormModal from '$lib/components/UserListFormModal.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { userListsRepository } from '$lib/repositories/userLists';
  import type { UserListType } from '$lib/types/userList';

  /** @type {import('./$types').LayoutData} */
  export let data;

  const userListsStore = writable(data.userLists);
  $: userListsStore.set(data.userLists);
  $: userLists = $userListsStore;

  $: selectedUserListId = $page.params.listId ?? null;

  let formOpen = false;
  let editingUserList: UserListType | null = null;
  let deleteTarget: UserListType | null = null;

  function openCreate() {
    editingUserList = null;
    formOpen = true;
  }

  function openEdit(userList: UserListType) {
    editingUserList = userList;
    formOpen = true;
  }

  async function refresh() {
    userListsStore.set(await userListsRepository.getAll());
  }

  async function onSaved() {
    formOpen = false;
    editingUserList = null;
    await refresh();
  }

  function requestDelete(userList: UserListType) {
    deleteTarget = userList;
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    const wasSelected = deleteTarget.id === selectedUserListId;
    await userListsRepository.delete(deleteTarget.id);
    deleteTarget = null;
    await refresh();
    if (wasSelected) await goto('/feed/lists', { noScroll: true, keepFocus: true });
  }

  function selectUserList(userListId: string) {
    goto(`/feed/lists/${userListId}`, { noScroll: true, keepFocus: true });
  }

  setContext('user-lists-panel', { userLists: userListsStore, openCreate, openEdit, requestDelete });
</script>

<Auth>
  <SideNav currentPage="lists" />
  <div class="feed-container">
    <div class="user-lists-layout">
      <div class="user-list-master">
        <div class="user-list-master-head">
          <h4>Lists</h4>
          <button class="user-list-master-add" on:click={openCreate} aria-label="New list">+</button>
        </div>

        {#if userLists.length === 0}
          <p class="user-list-master-empty">No lists yet.</p>
        {:else}
          {#each userLists as userList (userList.id)}
            <div
              class="user-list-row"
              class:selected={userList.id === selectedUserListId}
              role="button"
              tabindex="0"
              on:click={() => selectUserList(userList.id)}
              on:keydown={(e) => e.key === 'Enter' && selectUserList(userList.id)}
            >
              <span class="user-list-row-name">{userList.name}</span>
              <div class="user-list-row-actions">
                <button aria-label="Edit list" on:click|stopPropagation={() => openEdit(userList)}>
                  <span class="action-icon edit-icon"></span>
                </button>
                <button class="delete-btn" aria-label="Delete list" on:click|stopPropagation={() => requestDelete(userList)}>
                  <span class="action-icon delete-icon"></span>
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>

      <div class="user-list-detail">
        <slot />
      </div>
    </div>

    <BottomToolbar currentPage="lists" />
  </div>
</Auth>

{#if formOpen}
  <UserListFormModal userList={editingUserList} on:saved={onSaved} on:cancel={() => (formOpen = false)} />
{/if}

{#if deleteTarget}
  <ConfirmDialog
    title="Delete list?"
    message={`This will permanently delete "${deleteTarget.name}". This does not unfollow any members or delete any reeds.`}
    on:confirm={confirmDelete}
    on:cancel={() => (deleteTarget = null)}
  />
{/if}

<style>
  .feed-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .feed-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .feed-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .user-lists-layout {
    flex: 1;
    display: flex;
    min-height: 0;
  }

  .user-list-master {
    display: none;
  }

  @media (min-width: 900px) {
    .user-list-master {
      display: flex;
      flex-direction: column;
      gap: 0.6rem;
      width: 280px;
      flex-shrink: 0;
      border-right: 1px solid var(--border);
      padding: 1rem;
      overflow-y: auto;
    }
  }

  .user-list-master-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.2rem;
  }

  .user-list-master-head h4 {
    margin: 0;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .user-list-master-add {
    width: auto;
    flex-shrink: 0;
    margin-right: 1.25rem;
    background: none;
    border: none;
    color: var(--primary);
    font-size: 1.2rem;
    line-height: 1;
    cursor: pointer;
    padding: 0.1rem 0.3rem;
  }

  .user-list-master-empty {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .user-list-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0.65rem 0.9rem;
    cursor: pointer;
    transition: border-color 0.2s ease;
  }

  .user-list-row:hover {
    border-color: var(--primary);
  }

  .user-list-row.selected {
    border-color: var(--primary);
    background: rgba(88, 166, 255, 0.1);
  }

  .user-list-row-name {
    font-weight: 600;
    font-size: 0.88rem;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .user-list-row-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .user-list-row-actions button {
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.4rem;
    border-radius: 6px;
    color: var(--muted);
  }

  .user-list-row-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .user-list-row-actions button.delete-btn {
    color: var(--error);
  }

  .user-list-row-actions button.delete-btn:hover {
    background: var(--input-bg);
    color: var(--error);
  }

  .action-icon {
    display: inline-block;
    width: 1rem;
    height: 1rem;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .edit-icon {
    -webkit-mask-image: url('/icons/edit-16.png');
    mask-image: url('/icons/edit-16.png');
  }

  .delete-icon {
    -webkit-mask-image: url('/icons/trash-16.png');
    mask-image: url('/icons/trash-16.png');
  }

  .user-list-detail {
    flex: 1;
    min-width: 0;
  }
</style>
