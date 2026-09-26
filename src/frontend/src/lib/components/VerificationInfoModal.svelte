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
    aria-labelledby="verification-info-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <div class="modal-header">
        <h2 id="verification-info-title">What is verification?</h2>
        <button class="close-btn" aria-label="Close" on:click={close}>✕</button>
      </div>

      <p class="lead">
        Verifying someone means meeting them in person and scanning their code.
        It does two things at once.
      </p>

      <section class="purpose">
        <h3>It vouches for who they are</h3>
        <p>
          When you verify someone you are making a public claim: this account
          belongs to the person it says it does, and you can attest to that.
          It helps other people decide whether they trust this account.
        </p>
      </section>

      <section class="purpose">
        <h3>It pins down which key they use</h3>
        <p>
          Messages are encrypted to whatever key the server says is theirs. An
          attacker who takes over the server could swap in a key of their own
          and read encrypted messages and even impersonate them. Scanning their
          code means that their app tells yours which key they are using,
          directly, without going through the server, making it impossible for
          a malicious actor to swap them silently. The app would notice and
          alert you. This part is what protects you.
        </p>
      </section>

      <p class="foot">
        No check mark is not a bad sign. Most accounts have none; it only means
        nobody has verified them yet.
      </p>

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
    /* Above pages' floating action buttons (z-index: 1000) so the modal
       covers them instead of the button floating over the dialog. */
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
  .purpose p,
  .foot {
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

  .foot {
    color: var(--muted);
    border-top: 1px solid var(--border);
    padding-top: 0.75rem;
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
