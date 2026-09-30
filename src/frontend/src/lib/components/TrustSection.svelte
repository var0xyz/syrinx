<script lang="ts">
  import { onMount } from 'svelte';
  import TrustDetailsModal from '$lib/components/TrustDetailsModal.svelte';
  import VerificationInfoModal from '$lib/components/VerificationInfoModal.svelte';
  import {
    keyChangeFor,
    liveVouchesFor,
    reconcileVouches,
    staleVouchesFor,
    type KeyChangeKind,
  } from '$lib/services/vouches';
  import { isKeyChangeAlarm } from '$lib/utils/keyChange';
  import { vouchesChanged } from '$lib/stores/vouchChanges';
  import { userInfoRepository } from '$lib/repositories/userInfo';
  import type { VouchRecord } from '$lib/repositories/vouches';

  export let userID: string;
  export let activeKeyID: string | undefined = undefined;
  /** Ids the server reports for this user; the list this client reconciles. */
  export let vouchIDs: string[] = [];
  /** Bindable: set true to open the evidence modal (the mark does this). */
  export let showDetails = false;

  let live: VouchRecord[] = [];
  let stale: VouchRecord[] = [];
  let keyChange: KeyChangeKind | null = null;
  let showInfo = false;

  onMount(readLocal);

  // Local render and reconciliation are independent: the id list comes from
  // the profile response, and waiting for it would block marks this device
  // already verified.
  let lastLocal = '';
  $: void onLocalInputs(userID, activeKeyID, $vouchesChanged);
  let lastRemote = '';
  $: void onServerIDs(userID, vouchIDs);

  async function onLocalInputs(id: string, keyID: string | undefined, changed: number) {
    const key = `${id}:${keyID ?? ''}:${changed}`;
    if (key === lastLocal) return;
    lastLocal = key;
    await readLocal();
  }

  // Reconciliation needs the id list, not the key: the certs it fetches
  // are keyed by vouch id, and readLocal resolves the key itself.
  async function onServerIDs(id: string, ids: string[]) {
    if (!id) return;
    const key = `${id}:${ids.join(',')}`;
    if (key === lastRemote) return;
    lastRemote = key;
    await reconcileVouches(id, ids);
    await readLocal();
  }

  async function refresh() {
    await readLocal();
  }

  async function readLocal() {
    if (!userID) return;
    // On a first visit the profile has no key yet; the info cache usually
    // does, and waiting for the network would leave the section blank.
    const keyID = activeKeyID ?? (await userInfoRepository.get(userID))?.activeKeyID;
    if (!keyID) return;
    live = await liveVouchesFor(userID, keyID);
    stale = await staleVouchesFor(userID, keyID);
    keyChange = await keyChangeFor(userID, keyID);
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
  {keyChange}
  on:close={() => (showDetails = false)}
  on:explain={() => {
    showDetails = false;
    showInfo = true;
  }}
  on:changed={async () => {
    showDetails = false;
    await refresh();
  }}
/>

<VerificationInfoModal open={showInfo} on:close={() => (showInfo = false)} />

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
