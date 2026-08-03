<script>
  /** Subscribe on mount, unsubscribe on destroy. Use under `{#key}`. */
  import { onDestroy } from 'svelte';
  import { serverConnection } from '$lib/services/serverConnection';

  /** @type {string} */
  export let authorId;
  /** @type {string} */
  export let reedId;

  let alive = true;

  void serverConnection.subscribeReed(authorId, reedId).then(() => {
    if (!alive) serverConnection.unsubscribeReed(authorId, reedId);
  });

  onDestroy(() => {
    alive = false;
    serverConnection.unsubscribeReed(authorId, reedId);
  });
</script>
