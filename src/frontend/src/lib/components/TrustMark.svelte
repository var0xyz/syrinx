<script lang="ts">
  import { trustMarkFor, type TrustMark } from '$lib/services/vouches';

  /** Canonical id of the user the mark describes. */
  export let userID: string;
  /** The subject's current key. A mark only ever describes this key. */
  export let activeKeyID: string | undefined = undefined;
  /** Tapping opens the evidence. Off for rows that navigate elsewhere. */
  export let linked = true;

  let mark: TrustMark = 'none';

  // Reads the locally verified set only, so a feed of rows issues no
  // requests. Reconciliation happens on the profile, not here.
  $: void resolve(userID, activeKeyID);

  async function resolve(id: string, keyID: string | undefined) {
    if (!id || !keyID) {
      mark = 'none';
      return;
    }
    const next = await trustMarkFor(id, keyID);
    if (id === userID && keyID === activeKeyID) mark = next;
  }

  const labels: Record<Exclude<TrustMark, 'none'>, string> = {
    blue: 'You verified this key in person',
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
  <span
    class="trust-mark {mark}"
    class:linked
    title={labels[mark]}
    aria-label={labels[mark]}
    role="img"
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
  </span>
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
