<script>
  /** Subscribe on mount, unsubscribe on destroy. Use under `{#key}`. */
  import { onDestroy } from 'svelte';
  import { serverConnection } from '$lib/services/serverConnection';

  /** @type {string} */
  export let reedId;
  /** @type {() => void} */
  export let onSubscribeOk = () => {};
  /** @type {() => void} */
  export let onSubscribeFailed = () => {};

  let alive = true;

  void serverConnection.subscribeReed(reedId).then((subscribed) => {
    if (!alive) {
      serverConnection.unsubscribeReed(reedId);
      return;
    }
    if (subscribed) onSubscribeOk();
    else onSubscribeFailed();
  });

  onDestroy(() => {
    alive = false;
    serverConnection.unsubscribeReed(reedId);
  });
</script>
