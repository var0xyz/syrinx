<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { authService } from '$lib/services/auth';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';

  let isAuthenticated = false;

  onMount(async () => {
    try {
      const user = await authService.getCurrentUser();
      isAuthenticated = !!user;
    } catch {
      isAuthenticated = false;
    }
  });

  $: status = $page.status;
  $: isNotFound = status === 404;
  $: title = isNotFound ? 'Page not found' : 'Something went wrong';
  $: description = isNotFound
    ? "The page you're looking for doesn't exist or has been moved."
    : ($page.error?.message ?? 'An unexpected error occurred.');
  $: icon = isNotFound ? '🪹' : '⚠️';
</script>

<div class="error-page">
  <div class="error-content">
    <div class="error-state">
      <div class="error-icon">{icon}</div>
      <h1 class="error-status">{status}</h1>
      <h2>{title}</h2>
      <p>{description}</p>
      <button class="btn btn-primary" on:click={() => goto(isAuthenticated ? '/reeds' : '/')}>
        {isAuthenticated ? 'Go to reeds' : 'Go home'}
      </button>
    </div>
  </div>

  {#if isAuthenticated}
    <BottomToolbar currentPage="" />
  {/if}
</div>

<style>
  .error-page {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .error-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .error-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .error-icon {
    font-size: 4rem;
    margin-bottom: 1rem;
  }

  .error-status {
    margin: 0 0 0.5rem 0;
    color: var(--muted);
    font-size: 3rem;
    font-weight: 700;
    letter-spacing: -0.02em;
  }

  .error-state h2 {
    margin: 0 0 0.75rem 0;
    color: var(--fg);
    font-size: 1.25rem;
  }

  .error-state p {
    margin: 0 0 1.5rem 0;
    font-size: 0.95rem;
    line-height: 1.4;
  }

  .btn {
    padding: 0.75rem 1.5rem;
    border-radius: 8px;
    border: none;
    cursor: pointer;
    font-weight: 600;
    transition: opacity 0.2s ease;
  }

  .btn-primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn-primary:hover {
    opacity: 0.9;
  }

  @media (max-width: 768px) {
    .error-content {
      padding: 0.5rem;
    }

    .error-icon {
      font-size: 3rem;
    }

    .error-status {
      font-size: 2.5rem;
    }
  }
</style>
