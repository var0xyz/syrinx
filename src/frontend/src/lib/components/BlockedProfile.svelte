<script lang="ts">
  import Avatar from '$lib/components/Avatar.svelte';
  import type * as api from '$lib/types/api';

  /** The verified block of the viewer this page is showing. */
  export let cert: api.BlockCert;

  $: blockedOn = new Date((cert.serverSignature?.signedAt ?? 0) * 1000).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
</script>

<div class="blocked-card">
  <Avatar userID={cert.userId} size="4rem" />
  <p class="blocked-id">{cert.userId}</p>
  <h3>This user blocked you on {blockedOn}.</h3>
  <p class="muted">You can't see their profile or reeds.</p>
  <div class="blocked-actions">
    <slot name="actions" />
  </div>
</div>

<style>
  .blocked-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    padding: 2rem 1rem;
    text-align: center;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
  }

  .blocked-id {
    margin: 0;
    font-family: monospace;
    color: var(--muted);
    word-break: break-all;
  }

  h3 {
    margin: 0.5rem 0 0;
    color: var(--fg);
  }

  .muted {
    margin: 0;
    color: var(--muted);
  }

  .blocked-actions {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.5rem;
  }
</style>
