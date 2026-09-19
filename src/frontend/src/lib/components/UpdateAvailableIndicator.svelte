<script lang="ts">
  import { updateAvailable, applyUpdate } from '$lib/services/pwa';
</script>

{#if $updateAvailable}
  <div class="update-banner" role="status">
    <p>A new version of Syrinx is available.</p>
    <button type="button" on:click={applyUpdate}>Reload</button>
  </div>
{/if}

<style>
  .update-banner {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    background: linear-gradient(135deg, #6c5ce7, #4834d4);
    color: white;
    padding: 0.5rem 1rem;
    text-align: center;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
    animation: slideDown 0.3s ease-out;
  }

  .update-banner p {
    margin: 0;
    font-size: 0.9rem;
    opacity: 0.95;
  }

  .update-banner button {
    background: white;
    color: #4834d4;
    border: none;
    border-radius: 6px;
    padding: 0.3rem 0.9rem;
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
  }

  .update-banner button:hover {
    opacity: 0.9;
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
    .update-banner p {
      font-size: 0.8rem;
    }
  }
</style>
