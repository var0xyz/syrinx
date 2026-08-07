<script>
  /** Subscribe on mount, unsubscribe on destroy. Use under `{#key}`. */
  import { onDestroy } from 'svelte';
  import { serverConnection } from '$lib/services/serverConnection';

  /** @type {string} */
  export let authorId;
  /** @type {string} */
  export let reedId;
  /** @type {() => void} */
  export let onSubscribeOk = () => {};
  /** @type {() => void} */
  export let onSubscribeFailed = () => {};

  let alive = true;

  void serverConnection.subscribeReed(authorId, reedId).then((subscribed) => {
    if (!alive) {
      serverConnection.unsubscribeReed(authorId, reedId);
      return;
    }
    if (subscribed) onSubscribeOk();
    else onSubscribeFailed();
  });

  onDestroy(() => {
    alive = false;
    serverConnection.unsubscribeReed(authorId, reedId);
  });
</script>
