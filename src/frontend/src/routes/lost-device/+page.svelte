<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { serverInfo } from '$lib/services/serverInfo';
  import { canonicalRebindUserID, finishRebind, mintPendingRebind, rebindFile } from '$lib/services/rebind';
  import { pendingRebindRepository, type PendingRebindRecord } from '$lib/repositories/pendingRebind';

  let typedID = '';
  let pending: PendingRebindRecord | null = null;
  let minting = false;
  let checking = false;
  let status = '';
  let error = '';

  $: serverID = $serverInfo?.id ?? (typeof localStorage !== 'undefined' ? localStorage.getItem('serverId') : null) ?? '';
  $: grouped = pending ? (pending.fingerprint.match(/.{1,4}/g) ?? []).join(' ') : '';

  onMount(async () => {
    [pending] = await pendingRebindRepository.getAll();
  });

  async function mint(): Promise<void> {
    error = '';
    if (!typedID.trim() || !serverID) return;
    minting = true;
    try {
      pending = await mintPendingRebind(canonicalRebindUserID(typedID, serverID));
    } catch (err) {
      error = err instanceof Error ? err.message : 'Could not create a key.';
    } finally {
      minting = false;
    }
  }

  async function download(): Promise<void> {
    if (!pending) return;
    const file = await rebindFile(pending);
    const url = URL.createObjectURL(file);
    const a = document.createElement('a');
    a.href = url;
    a.download = file.name;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function finish(): Promise<void> {
    if (!pending) return;
    error = '';
    status = '';
    checking = true;
    try {
      if (await finishRebind(pending)) {
        await goto('/backup');
      } else {
        status = 'Not approved yet. Try again once an admin tells you it’s done.';
      }
    } catch (err) {
      error = err instanceof Error ? err.message : 'Could not finish recovery.';
    } finally {
      checking = false;
    }
  }

  async function startOver(): Promise<void> {
    if (pending) await pendingRebindRepository.delete(pending.userID);
    pending = null;
    status = '';
  }
</script>

<div class="container">
  <div class="card">
    <h1>Lost your device?</h1>

    {#if !pending}
      <p class="lead">
        If you have no backup of your key, an admin can give your account a new one. Enter your user ID; if
        you don’t know it, ask an admin.
      </p>
      <form on:submit|preventDefault={mint}>
        <label class="field">
          <span>User ID</span>
          <input type="text" bind:value={typedID} autocomplete="off" spellcheck="false" placeholder="k3x9" />
        </label>
        <button class="btn primary" type="submit" disabled={minting || !typedID.trim() || !serverID}>
          {minting ? 'Creating a key…' : 'Create a new key'}
        </button>
      </form>
    {:else}
      <p class="lead">
        Give this file to an admin, then read them the fingerprint below so they can check it’s yours. Keep
        this device: the key only works here.
      </p>
      <p class="user">{pending.userID}</p>
      <div class="fingerprint">
        <span>Fingerprint</span>
        <code>{grouped}</code>
      </div>
      <div class="actions">
        <button class="btn secondary" type="button" on:click={download}>Download {pending.userID}.pub.txt.gz</button>
        <button class="btn primary" type="button" on:click={finish} disabled={checking}>
          {checking ? 'Checking…' : 'Finish recovery'}
        </button>
      </div>
      {#if status}<p class="status">{status}</p>{/if}
      <button class="link" type="button" on:click={startOver}>Start over with another user ID</button>
    {/if}

    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
</div>

<style>
  .container {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    width: min(480px, 100%);
    display: flex;
    flex-direction: column;
    gap: 1rem;
    padding: 1.5rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }

  h1 {
    margin: 0;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .lead,
  .status {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .user {
    margin: 0;
    color: var(--fg);
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .fingerprint {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .fingerprint code {
    color: var(--fg);
    font-size: 1rem;
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .btn {
    width: auto;
    padding: 0.5rem 1rem;
    border-radius: 8px;
    border: none;
    font-family: inherit;
    font-weight: 600;
    cursor: pointer;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn.secondary {
    background: transparent;
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .link {
    width: auto;
    align-self: flex-start;
    padding: 0;
    border: none;
    background: none;
    color: var(--muted);
    font-size: 0.85rem;
    text-decoration: underline;
    cursor: pointer;
  }

  .error {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }
</style>
