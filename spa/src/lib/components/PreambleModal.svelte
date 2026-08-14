<script>
  import { createEventDispatcher } from 'svelte';
  import { canInstall, isInstalled, installPWA } from '$lib/services/pwa';

  export let open = false;

  const dispatch = createEventDispatcher();

  function accept() {
    dispatch('accept');
  }

  async function install() {
    await installPWA();
  }
</script>

{#if open}
  <div class="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="preamble-modal-title">
    <div class="modal">
      <h1 id="preamble-modal-title">Please read carefully</h1>

      <div class="content">
        <p class="intro">
          This is a <strong>Free</strong> (<a href="https://en.wikipedia.org/wiki/The_Free_Software_Definition">as defined by the Free Software Foundation</a>)
          web app, built on an open platform. You can inspect it and audit it, which is great for you as a user, but this also means that there are some
          inherent security &amp; privacy challenges to be aware of.
        </p>

        <div class="warning-section">
          <h2>Security &amp; Privacy Considerations</h2>

          <div class="warning-item">
            <h3>🛠️ Install the App</h3>
            <p>
              We strongly suggest installing the app before continuing. <strong>Browser extensions can
              read all your data</strong> unless the app is installed.
            </p>
          </div>

          <div class="warning-item">
            <h3>🌐 Data Permanence</h3>
            <p>
              The data you publish in the platform is not stored on the server, but on the users' devices.
              Think of it like a torrent. Once something is published there's no guarantee that it can be
              deleted. Assume that anything you publish will live somewhere forever.
            </p>
          </div>
        </div>
      </div>

      <p><strong>Important:</strong> If you create a user and later decide to install the app, you won't have
      access to that data anymore due to browser-enforced security restrictions. So either install the app first or
      use it as a web app, but migration won't be possible.</p>
      <div class="action-buttons">
        {#if $canInstall && !$isInstalled}
          <button on:click={install} class="btn btn-install">
            Install App
          </button>
        {/if}
        <button on:click={accept} class="btn btn-primary">I understand,<br>continue to sign up</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: var(--bg);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 300;
    padding: 1rem;
    overflow-y: auto;
  }

  .modal {
    max-width: 800px;
    width: 100%;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: left;
    margin: auto;
  }

  .modal h1 {
    margin: 0 0 1.5rem 0;
    color: var(--fg);
    font-size: 2rem;
    text-align: center;
  }

  .content {
    margin-bottom: 2rem;
  }

  .intro {
    font-size: 1.1rem;
    line-height: 1.6;
    margin-bottom: 2rem;
    text-align: center;
  }

  .warning-section {
    margin-bottom: 2rem;
  }

  .warning-section h2 {
    color: #f59e0b;
    font-size: 1.3rem;
    margin-bottom: 1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item {
    margin-bottom: 1.5rem;
    padding: 1rem;
    background: rgba(245, 158, 11, 0.1);
    border-left: 4px solid #f59e0b;
    border-radius: 8px;
  }

  .warning-item h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .warning-item p {
    margin: 0;
    line-height: 1.5;
  }

  .action-buttons {
    border-top: 1px solid var(--border);
    padding-top: 2rem;
    text-align: center;
    display: flex;
    gap: 1rem;
    justify-content: center;
    flex-wrap: wrap;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    transition: all 0.2s ease;
    border: none;
    cursor: pointer;
    font-size: 0.95rem;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  .btn-install {
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    color: white;
  }

  .btn-install:hover {
    opacity: 0.9;
    transform: translateY(-1px);
  }

  @media (max-width: 640px) {
    .modal {
      padding: 1.5rem;
    }

    .modal h1 {
      font-size: 1.5rem;
    }

    .action-buttons {
      flex-direction: column;
    }

    .btn {
      text-align: center;
      justify-content: center;
    }

    .warning-item {
      padding: 0.75rem;
    }

    .warning-item h3 {
      font-size: 1rem;
    }
  }
</style>
