<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import InvitesPanel from '$lib/components/InvitesPanel.svelte';
</script>

<Auth>
  <SideNav currentPage="invites" />
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

  @media (min-width: 768px) {
    .invites-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .invites-container {
      padding-right: var(--activity-sidebar-width);
    }
  }
</style>
