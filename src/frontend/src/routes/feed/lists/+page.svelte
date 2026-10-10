<script>
  import { getContext } from 'svelte';
  import { goto } from '$app/navigation';

  const { userLists, openCreate, openEdit, requestDelete } = getContext('user-lists-panel');
</script>

<div class="mobile-user-lists">
  <button class="floating-create-btn" on:click={openCreate} aria-label="New list">
    <span class="icon"></span>
  </button>

  {#if $userLists.length === 0}
    <div class="empty-state">
      <div class="empty-icon">📋</div>
      <h3>No lists yet</h3>
      <p>Create a list to organize the people you follow.</p>
    </div>
  {:else}
    <div class="user-lists">
      {#each $userLists as userList (userList.id)}
        <div
          class="user-list-row"
          role="button"
          tabindex="0"
          on:click={() => goto(`/feed/lists/${userList.id}`)}
          on:keydown={(e) => e.key === 'Enter' && goto(`/feed/lists/${userList.id}`)}
        >
          <span class="user-list-name">{userList.name}</span>
          <div class="user-list-actions">
            <button aria-label="Edit list" on:click|stopPropagation={() => openEdit(userList)}>
              <span class="action-icon edit-icon"></span>
            </button>
            <button class="delete-btn" aria-label="Delete list" on:click|stopPropagation={() => requestDelete(userList)}>
              <span class="action-icon delete-icon"></span>
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<div class="desktop-placeholder">
  <div class="empty-icon">📋</div>
  <p>Select a list to see its reeds.</p>
</div>

<style>
  .mobile-user-lists {
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  @media (min-width: 900px) {
    .mobile-user-lists {
      display: none;
    }
  }

  .desktop-placeholder {
    display: none;
  }

  @media (min-width: 900px) {
    .desktop-placeholder {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      height: 100%;
      color: var(--muted);
      text-align: center;
      gap: 0.5rem;
    }
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
    -webkit-mask-image: url('/icons/add-list-24.svg');
    mask-image: url('/icons/add-list-24.svg');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .user-lists {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .user-list-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.25rem 1rem;
    cursor: pointer;
    transition: all 0.2s ease;
  }

  .user-list-row:hover {
    border-color: var(--primary);
  }

  .user-list-name {
    font-weight: 600;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .user-list-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .user-list-actions button {
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

  .user-list-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .user-list-actions button.delete-btn {
    color: var(--error);
  }

  .user-list-actions button.delete-btn:hover {
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
    -webkit-mask-image: url('/icons/edit-16.svg');
    mask-image: url('/icons/edit-16.svg');
  }

  .delete-icon {
    -webkit-mask-image: url('/icons/trash-16.svg');
    mask-image: url('/icons/trash-16.svg');
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
    .mobile-user-lists {
      padding: 0.5rem;
    }
  }
</style>
