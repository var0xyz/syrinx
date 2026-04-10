<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { authService } from '$lib/services/auth';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';

  $: userId = $page.params.userId;

  let loggedInUserId = null;

  onMount(async () => {
    const currentUser = await authService.getCurrentUser().catch(() => null);
    loggedInUserId = currentUser?.id ?? null;
  });
</script>

<div class="reeds-container">
  <div class="reeds-content">
    <ReedsList authorId={userId} {loggedInUserId} showWriteButton={false} />
  </div>
  <BottomToolbar currentPage="reeds" />
</div>

<style>
  .reeds-container {
    min-height: 100vh;
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

  @media (max-width: 768px) {
    .reeds-content {
      padding: 0.5rem;
    }
  }
</style>
