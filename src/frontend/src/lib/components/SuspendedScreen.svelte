<script lang="ts">
  import type * as api from '$lib/types/api';
  import Username from '$lib/components/Username.svelte';
  import { formatAbsoluteDateTime, fromUnix } from '$lib/utils/time';

  export let request: api.AdminRequest;

  $: suspendedAt = fromUnix(request.serverSignature?.signedAt ?? 0);
</script>

<div class="suspended">
  <div class="panel">
    <span class="icon" aria-hidden="true"></span>
    <h1>Your account is suspended</h1>
    <blockquote class="reason">{request.reason}</blockquote>
    <p class="meta">
      Requested by <Username userID={request.requesterId} color="var(--primary)" />
      {#if request.decision}
        and approved by <Username userID={request.decision.deciderId} color="var(--primary)" />
      {/if}
      {#if suspendedAt}
        on {formatAbsoluteDateTime(suspendedAt)}
      {/if}
    </p>
    <p class="note">Everything on this device is kept. An admin can reinstate your account.</p>
  </div>
</div>

<style>
  .suspended {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
    background: var(--bg);
  }

  .panel {
    width: min(440px, 100%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
    padding: 1.5rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    text-align: center;
  }

  .icon {
    width: 2rem;
    height: 2rem;
    background-color: var(--error);
    -webkit-mask: url('/icons/suspend-16.svg') center / contain no-repeat;
    mask: url('/icons/suspend-16.svg') center / contain no-repeat;
  }

  h1 {
    margin: 0;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .reason {
    margin: 0;
    padding: 0.5rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    text-align: left;
    align-self: stretch;
  }

  .meta,
  .note {
    margin: 0;
    color: var(--muted);
    font-size: 0.85rem;
  }
</style>
