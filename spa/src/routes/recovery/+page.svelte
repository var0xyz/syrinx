<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import { isImportComplete } from '$lib/services/importRun';
  import { isRecoveryComplete, isRecoveryInProgress } from '$lib/services/recoveryRun';
  import { redirectForRestoreState } from '$lib/services/restoreFlow';

  onMount(() => {
    if (authService.isLoggedIn()) {
      goto('/reeds');
      return;
    }
    if (!isImportComplete()) {
      goto('/import');
      return;
    }
    if (isRecoveryComplete()) {
      goto('/reeds');
      return;
    }
    if (!isRecoveryInProgress()) {
      goto('/import');
    }
  });
</script>

<div class="container">
  <div class="card">
    <h1>Recover your account</h1>
    <p>
      Your backup is on this device. Account recovery will continue here —
      progress tools are coming next.
    </p>
    <a href="/" class="btn btn-secondary">Back to home</a>
  </div>
</div>

<style>
  .container {
    max-width: 640px;
    margin: 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 60vh;
    padding: 1rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2rem;
    text-align: center;
    width: min(480px, 100%);
  }

  .card h1 {
    margin: 0 0 1rem 0;
    color: var(--fg);
    font-size: 2rem;
  }

  .card p {
    margin: 0 0 2rem 0;
    color: var(--muted);
    font-size: 1.05rem;
    line-height: 1.5;
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
