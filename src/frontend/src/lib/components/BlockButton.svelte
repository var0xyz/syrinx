<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import { block, blocksChanged, isBlocking, unblock } from '$lib/services/blocks';
  import { notificationStore } from '$lib/stores/notifications';

  /** The user this button blocks or unblocks. */
  export let userID: string;
  /** Shown in the confirmation next to the ID. */
  export let username = '';
  /** Only ever offers Unblock, for lists of users already blocked. */
  export let unblockOnly = false;

  const dispatch = createEventDispatcher<{ unblocked: void }>();

  let blocking = unblockOnly;
  let confirming: 'block' | 'unblock' | null = null;
  let busy = false;

  async function refresh() {
    if (unblockOnly) return;
    blocking = await isBlocking(userID);
  }

  onMount(refresh);
  $: if ($blocksChanged >= 0 && userID) void refresh();

  async function confirmBlock() {
    confirming = null;
    busy = true;
    try {
      await block(userID);
    } catch (error) {
      console.error('[BlockButton] block failed', error);
      notificationStore.error('Could not block this user.');
    } finally {
      busy = false;
      await refresh();
    }
  }

  async function confirmUnblock() {
    confirming = null;
    busy = true;
    try {
      await unblock(userID);
      dispatch('unblocked');
    } catch (error) {
      console.error('[BlockButton] unblock failed', error);
      notificationStore.error('Could not unblock this user.');
    } finally {
      busy = false;
      await refresh();
    }
  }
</script>

{#if blocking}
  <button class="block-btn" disabled={busy} on:click={() => (confirming = 'unblock')}>Unblock</button>
{:else}
  <button class="block-btn danger" disabled={busy} on:click={() => (confirming = 'block')}>Block</button>
{/if}

{#if confirming}
  <div class="overlay" on:click={() => (confirming = null)} role="presentation"></div>
  <div class="modal" role="dialog" aria-modal="true" aria-labelledby="block-dialog-title">
    {#if confirming === 'block'}
      <h3 id="block-dialog-title">Block {username || 'this user'}?</h3>
      <p class="who">~{userID}</p>
      <ul>
        <li>They'll be told you blocked them.</li>
        <li>They'll be forced to delete everything of yours their device holds, and to unfollow you.</li>
        <li>They won't be able to see your profile or reeds, or follow you again.</li>
        <li>You'll still see theirs.</li>
      </ul>
      <p class="note">
        Unblocking later won't restore any of this: they won't follow you again and their copies
        of your content won't come back.
      </p>
      <div class="actions">
        <button class="btn btn-secondary" on:click={() => (confirming = null)}>Cancel</button>
        <button class="btn btn-danger" on:click={confirmBlock}>Block</button>
      </div>
    {:else}
      <h3 id="block-dialog-title">Unblock {username || 'this user'}?</h3>
      <p class="who">~{userID}</p>
      <p>
        They'll be able to see your profile and reeds again, and follow you. Nothing they had to
        delete comes back, and they won't be following you.
      </p>
      <div class="actions">
        <button class="btn btn-secondary" on:click={() => (confirming = null)}>Cancel</button>
        <button class="btn btn-primary" on:click={confirmUnblock}>Unblock</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .block-btn {
    width: auto;
    flex: none;
    padding: 0.5rem 1rem;
    border-radius: 8px;
    cursor: pointer;
    font-weight: 600;
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
    transition: all 0.2s ease;
  }

  .block-btn.danger {
    color: var(--error);
  }

  .block-btn:hover:not(:disabled) {
    background: var(--border);
  }

  .block-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

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
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    text-align: left;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: var(--fg);
    word-break: break-word;
  }

  ul {
    margin: 0;
    padding-left: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  ul,
  p {
    font-size: 0.9rem;
    line-height: 1.5;
    color: var(--fg);
  }

  p {
    margin: 0;
  }

  .note {
    color: var(--muted);
  }

  .who {
    margin-top: -0.5rem;
    font-family: monospace;
    font-size: 0.8rem;
    color: var(--muted);
    word-break: break-all;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    justify-content: flex-end;
    margin-top: 0.25rem;
  }

  .btn {
    width: auto;
    padding: 0.5rem 1.25rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    font-size: 0.9rem;
  }

  .btn-danger {
    background: var(--error);
    color: #fff;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
