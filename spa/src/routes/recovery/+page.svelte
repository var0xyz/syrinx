<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import ProgressBar from '$lib/components/ProgressBar.svelte';
  import { authService } from '$lib/services/auth';
  import { isImportComplete } from '$lib/services/importRun';
  import {
    ensureRecoveryProgress,
    getRecoveryProgressSummary,
    type RecoveryProgressSummary,
  } from '$lib/services/recoveryProgress';
  import { runRecoveryWork } from '$lib/services/recoveryRunner';
  import { isRecoveryComplete, isRecoveryInProgress } from '$lib/services/recoveryRun';

  let ready = false;
  let recoveryComplete = false;
  let running = false;
  let errorMessage = '';
  let summary: RecoveryProgressSummary = {
    completed: 0,
    total: 0,
    percent: 0,
    elapsedMs: 0,
  };

  function formatElapsed(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    const seconds = Math.floor(ms / 1000);
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    const rem = seconds % 60;
    return rem > 0 ? `${minutes}m ${rem}s` : `${minutes}m`;
  }

  function refreshSummary() {
    summary = getRecoveryProgressSummary();
  }

  async function runSteps() {
    if (running) return;
    running = true;
    errorMessage = '';
    try {
      const result = await runRecoveryWork({ onProgress: refreshSummary });
      refreshSummary();
      if (result.ok === false) {
        errorMessage = result.error;
      }
    } finally {
      running = false;
    }
  }

  onMount(() => {
    if (authService.isLoggedIn()) {
      goto('/reeds');
      return;
    }
    if (!isImportComplete()) {
      goto('/import');
      return;
    }

    recoveryComplete = isRecoveryComplete();
    if (recoveryComplete) {
      ready = true;
      refreshSummary();
      return;
    }
    if (!isRecoveryInProgress()) {
      goto('/import');
      return;
    }

    void (async () => {
      await ensureRecoveryProgress();
      refreshSummary();
      ready = true;
      await runSteps();
    })();
  });
</script>

<div class="container">
  <div class="card">
    <h1>Recover your account</h1>
    {#if recoveryComplete}
      <p>Recovery finished. Your account is ready to use on this server.</p>
      {#if summary.elapsedMs > 0}
        <p class="elapsed">Elapsed: {formatElapsed(summary.elapsedMs)}</p>
      {/if}
      <a href="/" class="btn btn-secondary">Back to home</a>
    {:else}
      <p>
        Your backup is on this device. Restoring your account on this server —
        this may take a moment.
      </p>

      {#if ready}
        <ProgressBar
          currentStep={summary.completed}
          totalSteps={Math.max(summary.total, 1)}
        />
        <p class="percent" aria-live="polite">{summary.percent}% complete</p>
        {#if summary.elapsedMs > 0}
          <p class="elapsed">Elapsed: {formatElapsed(summary.elapsedMs)}</p>
        {/if}
        {#if errorMessage}
          <p class="error" role="alert">{errorMessage}</p>
          <button
            type="button"
            class="btn btn-secondary"
            disabled={running}
            onclick={() => void runSteps()}
          >
            {running ? 'Retrying…' : 'Retry'}
          </button>
        {:else}
          <p class="hint">
            {#if running}
              Recovery steps are running…
            {:else}
              Recovery steps will continue automatically. You can leave this page
              open.
            {/if}
          </p>
        {/if}
      {:else}
        <p class="hint">Preparing recovery…</p>
      {/if}
    {/if}
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

  .card > p {
    margin: 0 0 1.5rem 0;
    color: var(--muted);
    font-size: 1.05rem;
    line-height: 1.5;
  }

  .percent {
    margin: 0.5rem 0 0 0 !important;
    color: var(--fg) !important;
    font-weight: 600;
    font-size: 1rem !important;
  }

  .elapsed {
    margin: 0.25rem 0 0 0 !important;
    color: var(--muted) !important;
    font-size: 0.95rem !important;
  }

  .hint {
    margin: 1rem 0 2rem 0 !important;
    color: var(--muted);
    font-size: 0.95rem !important;
  }

  .error {
    margin: 1rem 0 1rem 0 !important;
    color: var(--danger, #b42318) !important;
    font-size: 0.95rem !important;
    line-height: 1.4;
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

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .btn-secondary {
    background: var(--surface);
    color: var(--fg);
    border: 1px solid var(--border);
  }

  .btn-secondary:hover:not(:disabled) {
    background: var(--input-bg);
    transform: translateY(-1px);
  }
</style>
