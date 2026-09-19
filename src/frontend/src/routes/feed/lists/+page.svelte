<script lang="ts">
  import { goto } from '$app/navigation';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import FeedTabs from '$lib/components/FeedTabs.svelte';
  import ListFormModal from '$lib/components/ListFormModal.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { listsRepository } from '$lib/repositories/lists';
  import type { ListType } from '$lib/types/list';

  /** @type {import('./$types').PageData} */
  export let data;

  let lists = data.lists;
  $: lists = data.lists;

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
    lists = await listsRepository.getAll();
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
    await listsRepository.delete(deleteTarget.id);
    deleteTarget = null;
    await refresh();
  }
</script>

<Auth>
  <div class="feed-container">
    <FeedTabs active="list" />

    <div class="feed-content-wrap">
      <button
        class="floating-create-btn"
        on:click={openCreate}
        aria-label="New list"
      >
        <span class="icon"></span>
      </button>

      {#if lists.length === 0}
        <div class="empty-state">
          <div class="empty-icon">📋</div>
          <h3>No lists yet</h3>
          <p>Create a list to organize the people you follow.</p>
        </div>
      {:else}
        <div class="lists">
          {#each lists as list (list.id)}
            <div
              class="list-row"
              role="button"
              tabindex="0"
              on:click={() => goto(`/feed/lists/${list.id}`)}
              on:keydown={(e) => e.key === 'Enter' && goto(`/feed/lists/${list.id}`)}
            >
              <span class="list-name">{list.name}</span>
              <div class="list-actions">
                <button aria-label="Edit list" on:click|stopPropagation={() => openEdit(list)}>
                  <span class="action-icon edit-icon"></span>
                </button>
                <button class="delete-btn" aria-label="Delete list" on:click|stopPropagation={() => requestDelete(list)}>
                  <span class="action-icon delete-icon"></span>
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
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

  .feed-content-wrap {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .floating-create-btn {
    position: fixed;
    bottom: 5rem;
    right: 1.5rem;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .floating-create-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-create-btn .icon {
    display: inline-block;
    width: 1.5rem;
    height: 1.5rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/add-list-24.png');
    mask-image: url('/icons/add-list-24.png');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .lists {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .list-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.25rem 1rem;
    cursor: pointer;
    transition: all 0.2s ease;
  }

  .list-row:hover {
    border-color: var(--primary);
  }

  .list-name {
    font-weight: 600;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .list-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .list-actions button {
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.5rem;
    border-radius: 6px;
    color: var(--muted);
  }

  .list-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .list-actions button.delete-btn {
    color: var(--error);
  }

  .list-actions button.delete-btn:hover {
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

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }

  @media (max-width: 768px) {
    .feed-content-wrap {
      padding: 0.5rem;
    }
  }
</style>
