<script lang="ts">
  import { setContext } from 'svelte';
  import { writable } from 'svelte/store';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import FeedTabs from '$lib/components/FeedTabs.svelte';
  import ListFormModal from '$lib/components/ListFormModal.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { listsRepository } from '$lib/repositories/lists';
  import type { ListType } from '$lib/types/list';

  /** @type {import('./$types').LayoutData} */
  export let data;

  const listsStore = writable(data.lists);
  $: listsStore.set(data.lists);
  $: lists = $listsStore;

  $: selectedListId = $page.params.listId ?? null;

  let formOpen = false;
  let editingList: ListType | null = null;
  let deleteTarget: ListType | null = null;

  function openCreate() {
    editingList = null;
    formOpen = true;
  }

  function openEdit(list: ListType) {
    editingList = list;
    formOpen = true;
  }

  async function refresh() {
    listsStore.set(await listsRepository.getAll());
  }

  async function onSaved() {
    formOpen = false;
    editingList = null;
    await refresh();
  }

  function requestDelete(list: ListType) {
    deleteTarget = list;
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    const wasSelected = deleteTarget.id === selectedListId;
    await listsRepository.delete(deleteTarget.id);
    deleteTarget = null;
    await refresh();
    if (wasSelected) await goto('/feed/lists', { noScroll: true, keepFocus: true });
  }

  function selectList(listId: string) {
    goto(`/feed/lists/${listId}`, { noScroll: true, keepFocus: true });
  }

  setContext('lists-panel', { lists: listsStore, openCreate, openEdit, requestDelete });
</script>

<Auth>
  <SideNav currentPage="" />
  <div class="feed-container">
    <FeedTabs active="list" />

    <div class="lists-layout">
      <div class="list-master">
        <div class="list-master-head">
          <h4>Lists</h4>
          <button class="list-master-add" on:click={openCreate} aria-label="New list">+</button>
        </div>

        {#if lists.length === 0}
          <p class="list-master-empty">No lists yet.</p>
        {:else}
          {#each lists as list (list.id)}
            <div
              class="list-row"
              class:selected={list.id === selectedListId}
              role="button"
              tabindex="0"
              on:click={() => selectList(list.id)}
              on:keydown={(e) => e.key === 'Enter' && selectList(list.id)}
            >
              <span class="list-row-name">{list.name}</span>
              <div class="list-row-actions">
                <button aria-label="Edit list" on:click|stopPropagation={() => openEdit(list)}>
                  <span class="action-icon edit-icon"></span>
                </button>
                <button class="delete-btn" aria-label="Delete list" on:click|stopPropagation={() => requestDelete(list)}>
                  <span class="action-icon delete-icon"></span>
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>

      <div class="list-detail">
        <slot />
      </div>
    </div>

    <BottomToolbar currentPage="feeds" />
  </div>
</Auth>

{#if formOpen}
  <ListFormModal list={editingList} on:saved={onSaved} on:cancel={() => (formOpen = false)} />
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
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .feed-container {
      padding-left: 220px;
    }
  }

  .lists-layout {
    flex: 1;
    display: flex;
    min-height: 0;
  }

  .list-master {
    display: none;
  }

  @media (min-width: 900px) {
    .list-master {
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

  .list-master-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.2rem;
  }

  .list-master-head h4 {
    margin: 0;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .list-master-add {
    width: auto;
    flex-shrink: 0;
    background: none;
    border: none;
    color: var(--primary);
    font-size: 1.2rem;
    line-height: 1;
    cursor: pointer;
    padding: 0.1rem 0.3rem;
  }

  .list-master-empty {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .list-row {
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

  .list-row:hover {
    border-color: var(--primary);
  }

  .list-row.selected {
    border-color: var(--primary);
    background: rgba(88, 166, 255, 0.1);
  }

  .list-row-name {
    font-weight: 600;
    font-size: 0.88rem;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .list-row-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .list-row-actions button {
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

  .list-row-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .list-row-actions button.delete-btn {
    color: var(--error);
  }

  .list-row-actions button.delete-btn:hover {
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

  .list-detail {
    flex: 1;
    min-width: 0;
  }
</style>
