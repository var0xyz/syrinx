<script>
  import { identiconForUserId } from '$lib/utils/identicon';

  /** @type {string} */
  export let userID = '';

  /** @type {string} */
  export let username = '';

  /** @type {string} */
  export let size = '2.5rem';

  /** Circle crop (default) or full square. */
  /** @type {'circle' | 'square'} */
  export let shape = 'circle';

  /** @type {{ size: number, background: string, cells: (string | null)[][] } | null} */
  let model = null;
  let loadedFor = '';

  $: if (userID && userID !== loadedFor) {
    loadedFor = userID;
    model = null;
    identiconForUserId(userID).then((m) => {
      if (loadedFor === userID) model = m;
    });
  }
</script>

{#if userID}
  <span
    class="avatar"
    class:square={shape === 'square'}
    style="width: {size}; height: {size};"
    role="img"
    aria-label={username ? `${username}'s avatar` : 'Avatar'}
  >
    {#if model}
      <svg
        class="avatar-svg"
        viewBox="0 0 {model.size} {model.size}"
        xmlns="http://www.w3.org/2000/svg"
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
    border-radius: 50%;
    background: var(--input-bg);
    border: 1px solid var(--border);
    vertical-align: middle;
  }

  .avatar.square {
    border-radius: 8px;
  }

  .avatar-svg {
    width: 100%;
    height: 100%;
    display: block;
  }
</style>
