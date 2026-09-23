<script lang="ts">
  import { setContext } from 'svelte';
  import { writable } from 'svelte/store';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import OpenPipeModal from '$lib/components/OpenPipeModal.svelte';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { pipesRepository } from '$lib/repositories/pipes';
  import type { PipeType } from '$lib/types/pipe';

  /** @type {import('./$types').LayoutData} */
  export let data;

  const tabs = [
    { href: '/feed/follow', label: 'Following' },
    { href: '/feed/broadcast', label: 'Broadcast' },
    { href: '/feed/pipes', label: 'Pipes' },
  ];

  const pipesStore = writable(data.pipes);
  $: pipesStore.set(data.pipes);
  $: pipes = $pipesStore;

  $: selectedTag = $page.params.tag ?? null;

  let pipeModalOpen = false;
  let deleteTarget: PipeType | null = null;

  async function refresh() {
    pipesStore.set(await pipesRepository.getAll());
  }

  function requestRemove(pipe: PipeType) {
    deleteTarget = pipe;
  }

  async function confirmRemove() {
    if (!deleteTarget) return;
    const wasSelected = deleteTarget.tagName === selectedTag;
    await pipesRepository.unpin(deleteTarget.tagName);
    deleteTarget = null;
    await refresh();
    if (wasSelected) await goto('/feed/pipes', { noScroll: true, keepFocus: true });
  }

  function selectPipe(tagName: string) {
    goto(`/feed/pipes/${encodeURIComponent(tagName)}`, { noScroll: true, keepFocus: true });
  }

  setContext('pipes-panel', { pipes: pipesStore, refresh });
</script>

<Auth>
  <SideNav currentPage="feeds" />
  <div class="pipes-container">
    <SectionTabs {tabs} active="pipes" />

    <div class="pipes-layout">
      <div class="pipe-master">
        <div class="pipe-master-head">
          <h4>Pipes</h4>
          <button class="pipe-master-add" on:click={() => (pipeModalOpen = true)} aria-label="Open hashtag">+</button>
        </div>

        {#if pipes.length === 0}
          <p class="pipe-master-empty">No pipes yet.</p>
        {:else}
          {#each pipes as pipe (pipe.tagName)}
            <div
              class="pipe-row"
              class:selected={pipe.tagName === selectedTag}
              role="button"
              tabindex="0"
              on:click={() => selectPipe(pipe.tagName)}
              on:keydown={(e) => e.key === 'Enter' && selectPipe(pipe.tagName)}
            >
              <span class="pipe-row-name">#{pipe.displayName}</span>
              <div class="pipe-row-actions">
                <button
                  class="delete-btn"
                  aria-label="Remove pipe"
                  on:click|stopPropagation={() => requestRemove(pipe)}
                >
                  <span class="action-icon unpin-icon"></span>
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>

      <div class="pipe-detail">
        <slot />
      </div>
    </div>

    <button
      class="floating-pipe-btn"
      on:click={() => (pipeModalOpen = true)}
      aria-label="Open hashtag"
    >
      <span class="icon"></span>
    </button>

    <BottomToolbar currentPage="feeds" />
  </div>
</Auth>

{#if pipeModalOpen}
  <OpenPipeModal on:cancel={() => { pipeModalOpen = false; void refresh(); }} />
{/if}

{#if deleteTarget}
  <ConfirmDialog
    title="Remove pipe?"
    message={`This will unpin #${deleteTarget.displayName} from your Pipes list.`}
    confirmLabel="Remove"
    on:confirm={confirmRemove}
    on:cancel={() => (deleteTarget = null)}
  />
{/if}

<style>
  .pipes-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .pipes-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .pipes-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .pipes-layout {
    flex: 1;
    display: flex;
    min-height: 0;
  }

  .pipe-master {
    display: none;
  }

  @media (min-width: 900px) {
    .pipe-master {
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

  .pipe-master-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.2rem;
  }

  .pipe-master-head h4 {
    margin: 0;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .pipe-master-add {
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

  .pipe-master-empty {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .pipe-row {
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

  .pipe-row:hover {
    border-color: var(--primary);
  }

  .pipe-row.selected {
    border-color: var(--primary);
    background: rgba(88, 166, 255, 0.1);
  }

  .pipe-row-name {
    font-weight: 600;
    font-size: 0.88rem;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .pipe-row-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .pipe-row-actions button {
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

  .pipe-row-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .pipe-row-actions button.delete-btn {
    color: var(--error);
  }

  .pipe-row-actions button.delete-btn:hover {
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

  .unpin-icon {
    -webkit-mask-image: url('/icons/unpin-24.png');
    mask-image: url('/icons/unpin-24.png');
  }

  .pipe-detail {
    flex: 1;
    min-width: 0;
  }

  .floating-pipe-btn {
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

  @media (min-width: 900px) {
    .floating-pipe-btn {
      display: none;
    }
  }

  .floating-pipe-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-pipe-btn .icon {
    display: inline-block;
    width: 1.5rem;
    height: 1.5rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/hashtag-24.png');
    mask-image: url('/icons/hashtag-24.png');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }
</style>
