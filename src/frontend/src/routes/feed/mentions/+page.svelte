<script>
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SectionTabs from '$lib/components/SectionTabs.svelte';
  import MentionsList from '$lib/components/MentionsList.svelte';
  import { captureWindowScroll } from '$lib/utils/scrollSnapshot';
  import { clearUnread, unreadInteractions } from '$lib/stores/unreadInteractions';

  clearUnread('mentions');

  $: tabs = [
    { href: '/replies', label: 'Replies', unread: $unreadInteractions.replies },
    { href: '/ripples', label: 'Ripples' },
    { href: '/feed/mentions', label: 'Mentions' },
  ];

  /** @type {import('./$types').PageData} */
  export let data;

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
  <SideNav currentPage="interactions" />
  <div class="feed-container">
    <SectionTabs {tabs} active="mentions" />

    <div class="feed-content-wrap">
      <MentionsList items={data.items} {scrollRestoreY} />
    </div>

    <BottomToolbar currentPage="interactions" />
  </div>
</Auth>

<style>
  .feed-container {
    min-height: calc(100vh - 3rem - 1px);
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  @media (min-width: 768px) {
    .feed-container {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .feed-container {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .feed-content-wrap {
    flex: 1;
    max-width: 680px;
    margin: 0 auto;
    width: 100%;
    padding: 1rem;
  }

  @media (max-width: 768px) {
    .feed-content-wrap {
      padding: 0.5rem;
    }
  }
</style>
