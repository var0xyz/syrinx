<script lang="ts">
  import { page } from '$app/stores';
  import type { RemovedState } from '$lib/services/removedSession';

  // Lost on a reload: the device was wiped, so nothing else holds it.
  $: removed = $page.state as Partial<RemovedState>;
</script>

<div class="container">
  <div class="card">
    <h1>Account removed</h1>
    <p class="subtitle">
      {removed.kicked ? 'The admins of this community removed your account.' : 'Your account was removed.'}
    </p>

    {#if removed.reason}
      <div class="reason">
        <h2>{removed.kicked ? 'Their reason' : 'Your note'}</h2>
        <p>{removed.reason}</p>
      </div>
    {/if}

    <div class="item">
      <p>
        You've been signed out, and everything this device held for your account, keys included,
        has been deleted. Your profile now shows as removed to everyone else.
      </p>
    </div>

    <div class="action-buttons">
      <a href="/" class="btn btn-primary">Return to home</a>
    </div>
  </div>
</div>

<style>
  .container {
    max-width: 800px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: left;
  }

  .card h1 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 2rem;
    text-align: center;
  }

  .subtitle {
    margin: 0 0 1.5rem 0;
    color: var(--muted);
    font-size: 1.1rem;
    text-align: center;
  }

  .reason {
    margin-bottom: 1.5rem;
    padding: 1rem;
    border-radius: 8px;
    background: rgba(245, 158, 11, 0.1);
    border-left: 4px solid #f59e0b;
  }

  .reason h2 {
    margin: 0 0 0.5rem;
    font-size: 1rem;
    color: var(--muted);
  }

  .reason p {
    margin: 0;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .item p {
    margin: 0 0 2rem;
    line-height: 1.5;
  }

  .action-buttons {
    border-top: 1px solid var(--border);
    padding-top: 2rem;
    display: flex;
    justify-content: center;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    font-size: 0.95rem;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  @media (max-width: 640px) {
    .container {
      padding: 0.5rem;
    }

    .card {
      padding: 1.5rem;
    }

    .card h1 {
      font-size: 1.5rem;
    }
  }
</style>
