<script>
  import { goto } from '$app/navigation';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import FlagReedModal from '$lib/components/FlagReedModal.svelte';
  import { shareReed } from '$lib/utils/shareReed';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { flagsChanged, ownFlag } from '$lib/services/flags';

  /** Reed to share; omit to hide Share (e.g. ripples, which can't be shared). */
  export let reedRef = '';
  /** Whoever the card shows as author — Go to profile points here. */
  export let userID = '';
  export let username = '';
  export let content = '';
  export let showProfile = true;
  /** Off where the page already has its own Share button. */
  export let showShare = true;
  /** Appended after the shared options (pin, unlike, delete, …). */
  export let extraOptions = [];

  let flagging = false;
  /** @type {import('$lib/repositories/flags').StoredFlag | null} */
  let flag = null;

  // Root's reeds and the viewer's own can't be flagged.
  $: reedAuthor = reedRef ? parseCanonicalId(reedRef) : null;
  $: viewerID = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  $: canFlag =
    !!reedAuthor && !!viewerID && `${reedAuthor[0]}@${reedAuthor[1]}` !== viewerID && reedAuthor[0] !== '1';
  $: if (canFlag) void $flagsChanged, ownFlag(reedRef).then((v) => (flag = v));

  $: options = [
    ...(reedRef && showShare
      ? [{ label: 'Share', icon: '/icons/share-24.svg', onSelect: () => shareReed(reedRef, username || userID, content) }]
      : []),
    ...(showProfile && userID
      ? [{ label: 'Go to profile', icon: '/icons/user-16.svg', onSelect: () => goto(`/profile/${userID}`) }]
      : []),
    ...extraOptions,
    ...(canFlag
      ? [
          { label: flag ? 'Flagged' : 'Flag', icon: '/icons/flag-16.svg', onSelect: () => (flagging = true) },
        ]
      : []),
  ];
</script>

{#if options.length > 0}
  <KebabMenu {options} />
{/if}

{#if canFlag}
  <FlagReedModal open={flagging} reedID={reedRef} {flag} on:close={() => (flagging = false)} />
{/if}
