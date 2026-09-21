<script lang="ts">
  import { getContext } from 'svelte';
  import type { Writable } from 'svelte/store';
  import { goto } from '$app/navigation';
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
  import { pipesRepository } from '$lib/repositories/pipes';
  import type { PipeType } from '$lib/types/pipe';

  const { pipes, refresh } = getContext<{ pipes: Writable<PipeType[]>; refresh: () => Promise<void> }>(
    'pipes-panel'
  );

  let deleteTarget: PipeType | null = null;

  function requestRemove(pipe: PipeType) {
    deleteTarget = pipe;
  }

  async function confirmRemove() {
    if (!deleteTarget) return;
    await pipesRepository.unpin(deleteTarget.tagName);
    deleteTarget = null;
    await refresh();
  }
</script>

<div class="mobile-pipes">
  {#if $pipes.length === 0}
    <div class="empty-state">
      <div class="empty-icon">🪈</div>
      <h3>No pipes open</h3>
      <p>Tap the hashtag button to follow a live pipe of reeds by tag.</p>
    </div>
  {:else}
    <div class="pipes">
      {#each $pipes as pipe (pipe.tagName)}
        <div
          class="pipe-row"
          role="button"
          tabindex="0"
          on:click={() => goto(`/feed/pipes/${encodeURIComponent(pipe.tagName)}`)}
          on:keydown={(e) => e.key === 'Enter' && goto(`/feed/pipes/${encodeURIComponent(pipe.tagName)}`)}
        >
          <span class="pipe-name">#{pipe.tagName}</span>
          <div class="pipe-actions">
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
    </div>
  {/if}
</div>

<div class="desktop-placeholder">
  <div class="empty-icon">🪈</div>
  <p>Select a pipe to see its reeds.</p>
</div>

{#if deleteTarget}
  <ConfirmDialog
    title="Remove pipe?"
    message={`This will unpin #${deleteTarget.tagName} from your Pipes list.`}
    confirmLabel="Remove"
    on:confirm={confirmRemove}
    on:cancel={() => (deleteTarget = null)}
  />
{/if}

<style>
  .mobile-pipes {
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  @media (min-width: 900px) {
    .mobile-pipes {
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

  .pipes {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .pipe-row {
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

  .pipe-row:hover {
    border-color: var(--primary);
  }

  .pipe-name {
    font-weight: 600;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .pipe-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .pipe-actions button {
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

  .pipe-actions button:hover {
    background: var(--input-bg);
    color: var(--fg);
  }

  .pipe-actions button.delete-btn {
    color: var(--error);
  }

  .pipe-actions button.delete-btn:hover {
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
    .mobile-pipes {
      padding: 0.5rem;
    }
  }
</style>
