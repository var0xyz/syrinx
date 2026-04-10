<script>
  import { onMount } from 'svelte';
  import { authService } from '$lib/services/auth';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';

  let user = null;
  let loading = true;

  onMount(async () => {
    try {
      user = await authService.getCurrentUser();

      if (!user) {
        window.location.href = '/';
        return;
      }
    } catch (error) {
      console.error('Error getting user:', error);
      window.location.href = '/';
    } finally {
      loading = false;
    }
  });
</script>

{#if loading}
  <div class="container">
    <div class="card">
      <div class="loading">
        <h2>Loading reeds...</h2>
        <p>Please wait while we fetch your message threads.</p>
      </div>
    </div>
  </div>
{:else}
  <Auth>
    <div class="reeds-container">
      <div class="reeds-content">
        <ReedsList authorId={user.id} isOwner={true} showWriteButton={true} />
      </div>
      <BottomToolbar currentPage="reeds" />
    </div>
  </Auth>
{/if}

<style>
  .reeds-container {
    min-height: calc(100vh - 4rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  .reeds-content {
    flex: 1;
    max-width: 600px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
  }

  @media (max-width: 768px) {
    .reeds-content {
      padding: 0.5rem;
    }
  }
</style>
