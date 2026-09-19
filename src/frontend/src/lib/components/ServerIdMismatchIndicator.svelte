<script lang="ts">
  import { serverIdMismatch } from '$lib/services/serverInfo';
</script>

{#if $serverIdMismatch}
  <div class="mismatch-indicator" role="alert">
    <p>
      This server no longer matches the one this device trusted (was
      {$serverIdMismatch.known}, now {$serverIdMismatch.fetched}). Your
      account and data were not touched — if you didn't expect this, stop
      and verify with the operator before importing or logging in again.
    </p>
  </div>
{/if}

<style>
  .mismatch-indicator {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    background: linear-gradient(135deg, #d63031, #8b0000);
    color: white;
    padding: 0.5rem 1rem;
    text-align: center;
    z-index: 1002;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
    animation: slideDown 0.3s ease-out;
  }

  .mismatch-indicator p {
    margin: 0;
    font-size: 0.9rem;
    opacity: 0.95;
  }

  @keyframes slideDown {
    from {
      transform: translateY(-100%);
    }
    to {
      transform: translateY(0);
    }
  }

  @media (max-width: 768px) {
    .mismatch-indicator p {
      font-size: 0.8rem;
    }
  }
</style>
