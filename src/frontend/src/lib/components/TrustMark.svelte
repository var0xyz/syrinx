<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { trustMarkFor, refreshVouches, type TrustMark } from '$lib/services/vouches';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import { vouchesChanged } from '$lib/stores/vouchChanges';

  /** Canonical id of the user the mark describes. */
  export let userID: string;
  /** The subject's current key. A mark only ever describes this key.
   * Left unset, it is read from the local info cache, never fetched. */
  export let activeKeyID: string | undefined = undefined;
  /** Tapping opens the evidence. Off for rows that navigate elsewhere. */
  export let linked = true;
  /** Fetch the subject's vouches in the background. Off by default: only
   * the few single-subject placements should spend a request. */
  export let refresh = false;

  const dispatch = createEventDispatcher();

  let mark: TrustMark = 'none';

  // Draws from the local store first; `refresh` corrects it afterwards,
  // and the write notifies $vouchesChanged, which re-runs this.
  $: void resolve(userID, activeKeyID, $vouchesChanged);
  $: if (refresh && userID) void refreshVouches(userID);

  async function resolve(id: string, keyID: string | undefined, _changed: number) {
    if (!id) {
      mark = 'none';
      return;
    }
    // Cache-only: a feed of rows must not turn into a fetch per row.
    const key = keyID ?? (await userInfoRepository.get(id))?.activeKeyID;
    if (!key) {
      mark = 'none';
      return;
    }
    const next = await trustMarkFor(id, key);
    if (id === userID && keyID === activeKeyID) mark = next;
  }

  const labels: Record<Exclude<TrustMark, 'none'>, string> = {
    blue: 'You verified this person',
    green: 'Verified by someone you verified',
    grey: 'Verified by someone',
  };
  const shortLabels: Record<Exclude<TrustMark, 'none'>, string> = {
    blue: 'Verified by you',
    green: 'Verified by a contact',
    grey: 'Verified',
  };
</script>

{#if mark !== 'none'}
  <!-- Shape differs per level, so the three never rely on colour alone. -->
  <svelte:element
    this={linked ? 'button' : 'span'}
    class="trust-mark {mark}"
    class:linked
    title={labels[mark]}
    aria-label={linked ? `${labels[mark]} — show details` : labels[mark]}
    role={linked ? undefined : 'img'}
    type={linked ? 'button' : undefined}
    on:click={linked ? () => dispatch('open') : undefined}
  >
    <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true">
      {#if mark === 'blue'}
        <circle cx="12" cy="12" r="10" fill="currentColor" />
        <path d="M7 12.5l3.2 3.2L17 9" fill="none" stroke="#fff" stroke-width="2.4"
          stroke-linecap="round" stroke-linejoin="round" />
      {:else if mark === 'green'}
        <circle cx="12" cy="12" r="9.2" fill="none" stroke="currentColor" stroke-width="2.2" />
        <path d="M7.5 12.5l3.1 3.1L16.5 9.5" fill="none" stroke="currentColor" stroke-width="2.2"
          stroke-linecap="round" stroke-linejoin="round" />
      {:else}
        <path d="M6.5 12.6l3.4 3.4L17.5 8.4" fill="none" stroke="currentColor" stroke-width="2.2"
          stroke-linecap="round" stroke-linejoin="round" />
      {/if}
    </svg>
    <span class="sr-only">{shortLabels[mark]}</span>
  </svelte:element>
{/if}

<style>
  .trust-mark {
    display: inline-flex;
    align-items: center;
    vertical-align: -2px;
    margin-left: 0.25rem;
  }

  .trust-mark.blue {
    color: var(--accent, #1971c2);
  }

  .trust-mark.green {
    color: var(--success, #2f9e44);
  }

  .trust-mark.grey {
    color: var(--muted);
  }

  .trust-mark.linked {
    cursor: pointer;
    background: none;
    border: none;
    padding: 0;
    font: inherit;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
