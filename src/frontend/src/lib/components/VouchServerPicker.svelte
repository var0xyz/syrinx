<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { VouchServerChoice } from '$lib/types/server';

  export let open = false;
  export let servers: VouchServerChoice[] = [];

  const dispatch = createEventDispatcher<{ select: VouchServerChoice; close: void }>();

  function close() {
    dispatch('close');
  }
</script>

{#if open}
  <div
    class="modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="vouch-server-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <h2 id="vouch-server-title">Which server is the other person on?</h2>
      <p class="hint">The link has to open on their server, where they are signed in.</p>
      <ul class="servers">
        {#each servers as server (server.origin)}
          <li>
            <button class="server" on:click={() => dispatch('select', server)}>
              <span class="name">{server.name}</span>
              {#if server.isSelf}
                <span class="tag">This server</span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>
      <button class="btn secondary" on:click={close}>Cancel</button>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    /* Same layer as QRCodeModal, above floating action buttons. */
    z-index: 1100;
    padding: 1rem;
  }

  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.25rem;
    max-width: 480px;
    width: 100%;
  }

  .modal h2 {
    margin: 0 0 0.5rem 0;
    font-size: 1.2rem;
  }

  .hint {
    margin: 0 0 1rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .servers {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .server {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-size: 0.95rem;
    cursor: pointer;
    text-align: left;
  }

  .server:hover {
    border-color: var(--primary);
  }

  .name {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .tag {
    flex-shrink: 0;
    font-size: 0.75rem;
    color: var(--muted);
  }

  .btn {
    border: none;
    border-radius: 8px;
    padding: 0.6rem 1rem;
    font-weight: 600;
    cursor: pointer;
    width: 100%;
  }

  .btn.secondary {
    background: var(--input-bg);
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
