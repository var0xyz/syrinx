<script lang="ts">
  import { serverInfo } from '$lib/services/serverInfo';
  import { formatAbsoluteDateTime } from '$lib/utils/time';

  /** Class applied to the wrapping <p> — callers own layout/spacing. */
  export let className = '';
</script>

<p class={className}>
  {#if $serverInfo?.name}
    {$serverInfo.name}<br />
  {/if}
  Version {__APP_VERSION__.slice(0, 12)}<br />
  Built {formatAbsoluteDateTime(__APP_BUILD_TIME__)}
</p>
