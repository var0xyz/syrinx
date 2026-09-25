<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import CopyButton from '$lib/components/CopyButton.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { qrCodeDataURL } from '$lib/utils/qrCode';

  export let open = false;
  export let title = 'Invite link';
  export let url = '';
  /** Names what the link is, for copy confirmations and alt text. */
  export let subject = 'invite link';
  /** Optional line above the code explaining what to do with it. */
  export let hint = '';

  const dispatch = createEventDispatcher();

  $: dataURL = url ? qrCodeDataURL(url) : '';

  function close() {
    dispatch('close');
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(url);
      notificationStore.success(`Copied ${subject}`);
    } catch (err) {
      console.error(err);
      notificationStore.error(`Failed to copy ${subject}`);
    }
  }
</script>

{#if open}
  <div
    class="modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="qr-modal-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <h2 id="qr-modal-title">{title}</h2>
      {#if hint}
        <p class="qr-hint">{hint}</p>
      {/if}
      {#if dataURL}
        <div class="qr-wrap">
          <img class="qr-image" src={dataURL} alt={`QR code for ${subject}`} width="240" height="240" />
        </div>
      {/if}
      <div class="link-row">
        <code class="share-url">{url}</code>
        <CopyButton ariaLabel={`Copy ${subject}`} on:click={copyLink} />
      </div>
      <button class="btn primary" on:click={close}>Done</button>
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
    /* Above pages' floating action buttons (z-index: 1000) so the modal
       covers them instead of the button floating over the dialog. */
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
    margin: 0 0 0.75rem 0;
    font-size: 1.2rem;
  }

  .qr-wrap {
    display: flex;
    justify-content: center;
    margin-bottom: 1rem;
    padding: 1rem;
    background: #fff;
    border-radius: 8px;
  }

  .qr-image {
    width: 240px;
    height: 240px;
    image-rendering: pixelated;
  }

  .link-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 1rem;
  }

  .qr-hint {
    margin: 0 0 0.75rem;
    font-size: 0.85rem;
    color: var(--muted);
    text-align: center;
  }

  .share-url {
    flex: 1;
    min-width: 0;
    font-size: 0.8rem;
    word-break: break-all;
    background: var(--input-bg);
    padding: 0.5rem;
    border-radius: 6px;
  }

  .btn {
    border: none;
    border-radius: 8px;
    padding: 0.6rem 1rem;
    font-weight: 600;
    cursor: pointer;
    width: 100%;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }
</style>
