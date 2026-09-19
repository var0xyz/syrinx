<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import NetworkTabs from '$lib/components/NetworkTabs.svelte';
  import InvitesPanel from '$lib/components/InvitesPanel.svelte';
</script>

<Auth>
  <div class="network-container">
    <NetworkTabs active="users" />
    <InvitesPanel />
    <BottomToolbar currentPage="network" />
  </div>
</Auth>

<style>
  .network-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
</style>
