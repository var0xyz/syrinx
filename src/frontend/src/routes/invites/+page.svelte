<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import InvitesPanel from '$lib/components/InvitesPanel.svelte';
</script>

<Auth>
  <div class="invites-container">
    <InvitesPanel />
    <BottomToolbar currentPage="invites" />
  </div>
</Auth>

<style>
  .invites-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
</style>
