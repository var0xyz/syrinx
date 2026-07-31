<script>
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';

  /** @type {import('./$types').PageData} */
  export let data;

  $: user = data.user;
</script>

<Auth>
  <div class="reeds-container">
    <div class="reeds-content">
      <ReedsList authorId={user.id} isOwner={true} showWriteButton={true} />
    </div>
    <BottomToolbar currentPage="reeds" />
  </div>
</Auth>

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

  @media (max-width: 768px) {
    .reeds-content {
      padding: 0.5rem;
    }
  }
</style>
