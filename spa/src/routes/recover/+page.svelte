<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { get } from 'svelte/store';
  import { authService } from '$lib/services/auth';
  import { isRecoveryMode, serverInfoLoading } from '$lib/services/serverInfo';

  function checkAccess() {
    if (authService.isLoggedIn()) {
      goto('/reeds');
      return;
    }

    if (!get(serverInfoLoading) && !get(isRecoveryMode)) {
      goto('/');
    }
  }

  onMount(() => {
    checkAccess();
    return serverInfoLoading.subscribe(() => checkAccess());
  });
</script>

<div class="container">
  <div class="card">
    <h1>Recover your account</h1>
    <p>Recovery tools will be available here soon.</p>
    <a href="/" class="btn btn-secondary">Back to home</a>
  </div>
</div>

<style>
  .container {
    max-width: 640px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: center;
  }

  .card h1 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 2rem;
  }

  .card p {
    margin: 0 0 2rem 0;
    color: var(--muted);
    font-size: 1.1rem;
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
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover {
    background: var(--input-bg);
    transform: translateY(-1px);
  }
</style>
