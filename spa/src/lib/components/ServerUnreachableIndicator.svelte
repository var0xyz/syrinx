<script lang="ts">
  import { isServerUnreachable } from '$lib/services/serverInfo';
</script>

{#if $isServerUnreachable}
  <div class="unreachable-indicator" role="status">
    <p>Cannot reach this server. Your changes may not sync until the connection is restored.</p>
  </div>
{/if}

<style>
  .unreachable-indicator {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    background: linear-gradient(135deg, #e17055, #d63031);
    color: white;
    padding: 0.5rem 1rem;
    text-align: center;
    z-index: 1002;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
    animation: slideDown 0.3s ease-out;
  }

  .unreachable-indicator p {
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
    .unreachable-indicator p {
      font-size: 0.8rem;
    }
  }
</style>
