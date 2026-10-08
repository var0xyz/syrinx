<script lang="ts">
  import { formatBytes } from '$lib/utils/bytes';

  /** Null when the browser reports no estimate. */
  export let storage: { used: number; total: number } | null;
  /** Bytes a pending selection would free, drawn as part of the bar. */
  export let freeing = 0;

  $: used = storage?.used ?? 0;
  $: total = storage?.total ?? 0;
  $: percentage = total > 0 ? (used / total) * 100 : 0;
  $: freed = Math.min(freeing, used);
  $: freedPercentage = total > 0 ? (freed / total) * 100 : 0;
  $: level = percentage < 50 ? 'low-usage' : percentage < 80 ? 'medium-usage' : 'high-usage';
</script>

{#if storage}
  <div class="storage-info">
    <div class="storage-stats">
      <div class="storage-item">
        <span class="label">Used</span>
        <span class="value">{formatBytes(used)}</span>
      </div>
      <div class="storage-item">
        <span class="label">Total</span>
        <span class="value">{formatBytes(total)}</span>
      </div>
      <div class="storage-item">
        <span class="label">Percentage</span>
        <span class="value storage-percentage {level}">{percentage.toFixed(1)}%</span>
      </div>
    </div>
    <div class="storage-progress">
      <div class="progress-bar">
        <div class="progress-fill {level}" style="width: {percentage - freedPercentage}%"></div>
        {#if freed > 0}
          <div class="progress-freed" style="left: {percentage - freedPercentage}%; width: {freedPercentage}%"></div>
        {/if}
      </div>
      <div class="progress-labels">
        <span>0%</span>
        <span>100%</span>
      </div>
    </div>
  </div>
{:else}
  <div class="storage-unavailable">
    <p>Storage information is not available in this browser.</p>
  </div>
{/if}

<style>
  .storage-info {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .storage-stats {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 1rem;
  }

  .storage-item {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    text-align: center;
  }

  .label {
    color: var(--muted);
    font-size: 0.8rem;
    font-weight: 500;
  }

  .value {
    color: var(--fg);
    font-size: 0.9rem;
  }

  .storage-percentage {
    font-weight: 600;
  }

  .storage-percentage.low-usage {
    color: #4caf50;
  }

  .storage-percentage.medium-usage {
    color: #ff9800;
  }

  .storage-percentage.high-usage {
    color: #f44336;
  }

  .storage-progress {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .progress-bar {
    width: 100%;
    height: 12px;
    background-color: var(--input-bg);
    border-radius: 6px;
    overflow: hidden;
    position: relative;
  }

  .progress-fill {
    height: 100%;
    transition: width 0.3s ease;
    border-radius: 6px;
  }

  .progress-fill.low-usage {
    background: linear-gradient(90deg, #4caf50, #66bb6a);
  }

  .progress-fill.medium-usage {
    background: linear-gradient(90deg, #ff9800, #ffb74d);
  }

  .progress-fill.high-usage {
    background: linear-gradient(90deg, #f44336, #ef5350);
  }

  /* The share a selection would free: still used, so drawn hatched. */
  .progress-freed {
    position: absolute;
    top: 0;
    height: 100%;
    background: repeating-linear-gradient(45deg, var(--muted) 0 3px, transparent 3px 6px);
    opacity: 0.6;
    transition: left 0.3s ease, width 0.3s ease;
  }

  .progress-labels {
    display: flex;
    justify-content: space-between;
    font-size: 0.7rem;
    color: var(--muted);
  }

  .storage-unavailable {
    text-align: center;
    padding: 1rem;
    color: var(--muted);
  }

  .storage-unavailable p {
    margin: 0;
    font-style: italic;
  }

  @media (max-width: 768px) {
    .storage-stats {
      grid-template-columns: 1fr;
      gap: 0.75rem;
    }

    .storage-item {
      text-align: left;
    }
  }
</style>
