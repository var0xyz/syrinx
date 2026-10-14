<script lang="ts">
  import { page } from '$app/stores';
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import ModeSwitch from '$lib/components/ModeSwitch.svelte';
  import AdminRequestList from '$lib/components/AdminRequestList.svelte';
  import { isMailRequest } from '$lib/services/admin';
  import { keepInHash } from '$lib/utils/hashMode';
  import { adminPermissions, adminTabs, canMail } from '$lib/stores/adminPermissions';

  $: tabs = adminTabs(canMail($page.data?.user, $adminPermissions));

  const modeOptions = [
    { value: 'open', label: 'Open' },
    { value: 'closed', label: 'Closed' },
  ];

  let mode: 'open' | 'closed' = $page.url.hash === '#closed' ? 'closed' : 'open';
  $: keepInHash($page.url, mode === 'closed' ? '#closed' : '');

  $: isAdmin = $page.data?.user?.role === 'admin' || $page.data?.user?.role === 'root';
</script>

<Auth>
  <SideNav currentPage="admin" />
  <div class="admin-container">
    <SectionTabs {tabs} active="actions" />
    <div class="admin-content">
      {#if !isAdmin}
        <p class="error" role="alert">Admin access required.</p>
      {:else}
        <div class="mode-picker">
          <ModeSwitch options={modeOptions} bind:value={mode} />
        </div>

        <AdminRequestList
          include={(r) => !isMailRequest(r)}
          state={mode}
          emptyTitle={mode === 'open' ? 'Nothing to review' : 'No closed requests'}
        />
      {/if}
    </div>

    <BottomToolbar currentPage="admin" />
  </div>
</Auth>

<style>
  .admin-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .admin-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .admin-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .admin-content {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  .mode-picker {
    display: flex;
    justify-content: center;
    margin-bottom: 1rem;
  }

  .error {
    color: #b91c1c;
  }
</style>
