<script lang="ts">
  import Auth from '$lib/components/Auth.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
</script>

<Auth>
  <SideNav currentPage="interactions" />
  <div class="ripples-container">
    <div class="empty-state">
      <div class="empty-icon">🌊</div>
      <h3>Ripples</h3>
      <p>Coming soon.</p>
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

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }
</style>
