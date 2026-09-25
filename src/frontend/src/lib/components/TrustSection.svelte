<script lang="ts">
  import { onMount } from 'svelte';
  import TrustDetailsModal from '$lib/components/TrustDetailsModal.svelte';
  import {
    keyChangeFor,
    liveVouchesFor,
    reconcileVouches,
    staleVouchesFor,
    type KeyChangeKind,
  } from '$lib/services/vouches';
  import { isKeyChangeAlarm } from '$lib/utils/keyChange';
  import { trustRootsRepository } from '$lib/repositories/trustRoots';
  import type { VouchRecord } from '$lib/repositories/vouches';

  export let userID: string;
  export let activeKeyID: string | undefined = undefined;
  /** Ids the server reports for this user; the list this client reconciles. */
  export let vouchIDs: string[] = [];
  /** Bindable: set true to open the evidence modal (the mark does this). */
  export let showDetails = false;

  let live: VouchRecord[] = [];
  let stale: VouchRecord[] = [];
  let rootIDs = new Set<string>();
  let keyChange: KeyChangeKind | null = null;

  onMount(readLocal);

  // Local render and reconciliation are independent: the id list comes from
  // the profile response, and waiting for it would block marks this device
  // already verified.
  let lastLocal = '';
  $: void onLocalInputs(userID, activeKeyID);
  let lastRemote = '';
  $: void onServerIDs(userID, activeKeyID, vouchIDs);

  async function onLocalInputs(id: string, keyID: string | undefined) {
    const key = `${id}:${keyID ?? ''}`;
    if (key === lastLocal) return;
    lastLocal = key;
    await readLocal();
  }

  async function onServerIDs(id: string, keyID: string | undefined, ids: string[]) {
    if (!id || !keyID) return;
    const key = `${id}:${keyID}:${ids.join(',')}`;
    if (key === lastRemote) return;
    lastRemote = key;
    await reconcileVouches(id, ids);
    await readLocal();
  }

  async function refresh() {
    await readLocal();
  }

  async function readLocal() {
    if (!userID || !activeKeyID) return;
    live = await liveVouchesFor(userID, activeKeyID);
    stale = await staleVouchesFor(userID, activeKeyID);
    rootIDs = await trustRootsRepository.activeIDs();
    keyChange = await keyChangeFor(userID, activeKeyID);
  }

  // The substitution alarm is the one thing that stays on the profile: it
  // warns about evidence the user did not go looking for.
  $: alarm = isKeyChangeAlarm(keyChange);
</script>

{#if alarm}
  <section class="trust">
    <p class="row alarm">
      This account’s key changed and the change is not signed by the previous
      key. Do not treat this account as verified.
      <button class="link-btn" on:click={() => (showDetails = true)}>Details</button>
    </p>
  </section>
{/if}

<TrustDetailsModal
  open={showDetails}
  {userID}
  {live}
  {stale}
  {rootIDs}
  {keyChange}
  on:close={() => (showDetails = false)}
  on:changed={async () => {
    showDetails = false;
    await refresh();
  }}
/>

<style>
  .trust {
    border-top: 1px solid var(--border);
    margin-top: 1rem;
    padding-top: 0.75rem;
  }

  .row {
    font-size: 0.85rem;
    line-height: 1.5;
    margin: 0 0 0.4rem;
  }

  .row.alarm {
    color: var(--error, #e03131);
    font-weight: 500;
  }

  .link-btn {
    background: none;
    border: none;
    padding: 0;
    font-size: inherit;
    color: var(--accent, #1971c2);
    cursor: pointer;
    text-decoration: underline;
  }
</style>
