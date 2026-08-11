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
    aria-labelledby="reed-stats-info-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <div class="modal-header">
        <h2 id="reed-stats-info-title">Reed stats</h2>
        <button class="close-btn" aria-label="Close" on:click={close}>✕</button>
      </div>
      <dl class="stats-list">
        <div class="stats-row">
          <dt>
            <span class="stats-icon echoes" aria-hidden="true"></span>
            Echoes
          </dt>
          <dd>How many times this reed has been echoed by other users.</dd>
        </div>
        <div class="stats-row">
          <dt>
            <span class="stats-icon replies" aria-hidden="true"></span>
            Replies
          </dt>
          <dd>How many replies this conversation has.</dd>
        </div>
        <div class="stats-row">
          <dt>
            <span class="stats-icon likes" aria-hidden="true"></span>
            Likes
          </dt>
          <dd>How many users have liked this reed.</dd>
        </div>
        <div class="stats-row">
          <dt>
            <span class="stats-icon coverage" aria-hidden="true"></span>
            Coverage
          </dt>
          <dd>The percentage of users in the network that have a copy of this reed.</dd>
        </div>
      </dl>
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
    z-index: 200;
    padding: 1rem;
  }

  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.25rem 0.75rem 0.25rem 1rem;
    max-width: 420px;
    width: 100%;
  }

  .modal-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 1rem;
  }

  .modal-header h2 {
    flex: 1;
    margin: 0;
    font-size: 1.2rem;
    color: var(--fg);
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

  .stats-list {
    margin: 0;
  }

  .stats-row {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.6rem 0;
    border-bottom: 1px solid var(--border);
  }

  .stats-row:last-child {
    border-bottom: none;
  }

  .stats-row dt {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 600;
    color: var(--fg);
  }

  .stats-row dd {
    margin: 0;
    padding-left: 1.65rem;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .stats-icon {
    display: inline-block;
    width: 1.15rem;
    height: 1.15rem;
    flex-shrink: 0;
    background-color: currentColor;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }

  .stats-icon.echoes {
    -webkit-mask-image: url('/icons/megaphone-24.png');
    mask-image: url('/icons/megaphone-24.png');
  }

  .stats-icon.replies {
    -webkit-mask-image: url('/icons/reply-24.png');
    mask-image: url('/icons/reply-24.png');
  }

  .stats-icon.coverage {
    -webkit-mask-image: url('/icons/graph-24.png');
    mask-image: url('/icons/graph-24.png');
  }

  .stats-icon.likes {
    -webkit-mask-image: url('/icons/like-24-filled.png');
    mask-image: url('/icons/like-24-filled.png');
  }
</style>
