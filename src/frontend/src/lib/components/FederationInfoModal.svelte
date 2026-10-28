<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let open = false;

  const dispatch = createEventDispatcher();

  function close() {
    dispatch('close');
  }
</script>

{#if open}
  <div
    class="modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="federation-info-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <div class="modal-header">
        <h2 id="federation-info-title">About federation</h2>
        <button class="close-btn" aria-label="Close" on:click={close}>✕</button>
      </div>

      <p class="lead">
        Federation connects this server with another community&apos;s server.
        Once connected, your users can follow theirs, see their reeds and
        reply to them, and theirs can do the same with yours.
      </p>

      <p class="lead">However, there are also risks to federating.</p>

      <section class="purpose">
        <h3>It can overload your server</h3>
        <p>
          Connected servers exchange requests constantly. A server with many
          active users can overload a smaller one, so ask the other admin
          about their hardware and load before connecting.
        </p>
      </section>

      <section class="purpose">
        <h3>It can expose your community</h3>
        <p>
          Connected servers are notified of every new reed. A malicious actor
          posing as a like-minded community could fetch, store and leak every
          reed your users create. They could also find new attacks nobody has
          thought of yet.
        </p>
      </section>

      <section class="purpose">
        <h3>Only connect with people you trust</h3>
        <p>
          Make sure you trust the admins of the community you connect with.
          Ideally, you know them in person.
        </p>
      </section>

      <button class="btn primary" on:click={close}>Got it</button>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    /* Above the mesh page's .speed-dial (z-index: 1000). */
    z-index: 1100;
    padding: 1rem;
  }

  .modal {
    background: var(--bg, #fff);
    border-radius: 8px;
    padding: 1.25rem;
    max-width: 28rem;
    width: 100%;
    max-height: 85vh;
    overflow-y: auto;
  }

  .modal-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .modal-header h2 {
    flex: 1;
    font-size: 1rem;
    margin: 0;
  }

  .close-btn {
    flex-shrink: 0;
    width: 2rem;
    height: 2rem;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: var(--muted);
    font-size: 1rem;
    cursor: pointer;
    border-radius: 6px;
    transition: color 0.2s ease, background 0.2s ease;
  }

  .close-btn:hover {
    color: var(--fg);
    background: var(--input-bg);
  }

  .lead,
  .purpose p {
    font-size: 0.85rem;
    line-height: 1.5;
    margin: 0;
  }

  .lead {
    margin-bottom: 0.85rem;
  }

  .purpose {
    margin: 0 0 0.85rem;
  }

  .purpose h3 {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
    margin: 0 0 0.35rem;
  }

  .btn {
    margin-top: 0.75rem;
    padding: 0.4rem 1rem;
    border: none;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .btn.primary {
    background: var(--accent, #1971c2);
    color: #fff;
  }
</style>
