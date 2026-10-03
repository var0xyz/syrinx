<script>
  import { goto } from '$app/navigation';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import { shareReed } from '$lib/utils/shareReed';

  /** Reed to share; omit to hide Share (e.g. ripples, which can't be shared). */
  export let reedRef = '';
  /** Whoever the card shows as author — Go to profile points here. */
  export let userID = '';
  export let username = '';
  export let content = '';
  export let showProfile = true;
  /** Appended after the shared options (pin, unlike, delete, …). */
  export let extraOptions = [];

  $: options = [
    ...(reedRef
      ? [{ label: 'Share', icon: '/icons/share-24.png', onSelect: () => shareReed(reedRef, username || userID, content) }]
      : []),
    ...(showProfile && userID
      ? [{ label: 'Go to profile', icon: '/icons/user-16.svg', onSelect: () => goto(`/profile/${userID}`) }]
      : []),
    ...extraOptions,
  ];
</script>

{#if options.length > 0}
  <KebabMenu {options} />
{/if}
