<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let open = false;
  /** Whether the viewer can create admin invites, and so needs to know why they expire. */
  export let showAdmin = false;

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
    aria-labelledby="invite-info-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <div class="modal-header">
        <h2 id="invite-info-title">About invites</h2>
        <button class="close-btn" aria-label="Close" on:click={close}>✕</button>
      </div>

      <p class="lead">
        An invite is how someone joins this server, and creating one says that
        you trust the person who uses it.
      </p>

      {#if showAdmin}
        <section class="purpose">
          <h3>Why admin invites expire</h3>
          <p>
            An admin invite hands out admin powers. If the link leaked, whoever
            found it could claim them, so it stops working 24 hours after you
            create it. A user invite grants much less, so it doesn't expire.
          </p>
        </section>
      {/if}

      <section class="purpose">
        <h3>Why revoked invites still count</h3>
        <p>
          Revoking an invite doesn't give it back: every invite you create
          counts towards your limit. Take them seriously, and only create them
          for people you trust.
        </p>
      </section>

      <section class="purpose">
        <h3>You answer for who you invite</h3>
        <p>
          If someone you invited misbehaves, it reflects on you too, and the
          admins may decide to act against both of you. Be careful who you
          invite.
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
    /* Above the new invite modal (z-index: 1100) it opens from. */
    z-index: 1200;
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
