<script>
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import ReedsList from '$lib/components/ReedsList.svelte';
  import { captureWindowScroll } from '$lib/utils/scrollSnapshot';

  /** @type {import('./$types').PageData} */
  export let data;

  $: user = data.user;

  /** @type {number | null} */
  let scrollRestoreY = null;

  /** @type {import('./$types').Snapshot<number>} */
  export const snapshot = {
    capture: () => captureWindowScroll(),
    restore: (y) => {
      scrollRestoreY = y;
    },
  };
</script>

<Auth>
  <div class="reeds-container">
    <div class="reeds-content">
      <ReedsList authorId={user.id} isOwner={true} showWriteButton={true} {scrollRestoreY} />
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
