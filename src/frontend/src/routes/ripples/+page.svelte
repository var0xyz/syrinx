<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import RipplesInboxList from '$lib/components/RipplesInboxList.svelte';

  const tabs = [
    { href: '/replies', label: 'Replies' },
    { href: '/ripples', label: 'Ripples' },
    { href: '/feed/mentions', label: 'Mentions' },
  ];
</script>

<Auth>
  <SideNav currentPage="interactions" />
  <div class="ripples-container">
    <SectionTabs {tabs} active="ripples" />

    <div class="ripples-content-wrap">
      <RipplesInboxList />
    </div>

    <BottomToolbar currentPage="interactions" />
  </div>
</Auth>

<style>
  .ripples-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .ripples-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .ripples-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .ripples-content-wrap {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  @media (max-width: 768px) {
    .ripples-content-wrap {
      padding: 0.5rem;
    }
  }
</style>
