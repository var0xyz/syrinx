<script>
  import { identiconForUser } from '$lib/utils/identicon';

  /** @type {string} */
  export let userID = '';

  /** @type {string} */
  export let serverID = '';

  /** @type {string} */
  export let username = '';

  /** @type {string} */
  export let size = '2.5rem';

  /** @type {{ size: number, background: string, cells: (string | null)[][] } | null} */
  let model = null;
  let loadedFor = '';

  $: effectiveServerID =
    serverID ||
    (typeof localStorage !== 'undefined' ? localStorage.getItem('serverId') : '') ||
    '';
  $: identityKey = userID && effectiveServerID ? `${userID}@${effectiveServerID}` : '';

  $: if (identityKey && identityKey !== loadedFor) {
    loadedFor = identityKey;
    model = null;
    identiconForUser(userID, effectiveServerID).then((m) => {
      if (loadedFor === identityKey) model = m;
    });
  }
</script>

{#if userID && effectiveServerID}
  <span
    class="avatar"
    style="width: {size}; height: {size};"
    role="img"
    aria-label={username ? `${username}'s avatar` : 'Avatar'}
  >
    {#if model}
      <svg
        class="avatar-svg"
        viewBox="0 0 {model.size} {model.size}"
        xmlns="http://www.w3.org/2000/svg"
        shape-rendering="crispEdges"
        aria-hidden="true"
      >
        <rect width={model.size} height={model.size} fill={model.background} />
        {#each model.cells as row, r}
          {#each row as fill, c}
            {#if fill}
              <rect x={c} y={r} width="1" height="1" {fill} />
            {/if}
          {/each}
        {/each}
      </svg>
    {/if}
  </span>
{/if}

<style>
  .avatar {
    display: inline-flex;
    flex-shrink: 0;
    overflow: hidden;
    border-radius: 8px;
    background: var(--input-bg);
    vertical-align: middle;
  }

  .avatar-svg {
    width: 100%;
    height: 100%;
    display: block;
  }
</style>
