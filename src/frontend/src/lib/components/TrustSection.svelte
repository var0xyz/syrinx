<script lang="ts">
  import { onMount } from 'svelte';
  import Username from '$lib/components/Username.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import {
    liveVouchesFor,
    reconcileVouches,
    staleVouchesFor,
    withdrawVouch,
  } from '$lib/services/vouches';
  import { trustRootsRepository } from '$lib/repositories/trustRoots';
  import type { VouchRecord } from '$lib/repositories/vouches';

  export let userID: string;
  export let activeKeyID: string | undefined = undefined;
  /** Ids the server reports for this user; the list this client reconciles. */
  export let vouchIDs: string[] = [];

  let reconciled = false;
  let live: VouchRecord[] = [];
  let stale: VouchRecord[] = [];
  let rootIDs = new Set<string>();
  let withdrawing = '';

  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;

  onMount(refresh);
  $: void refreshOn(userID, activeKeyID, vouchIDs);

  let lastKey = '';
  async function refreshOn(id: string, keyID: string | undefined, ids: string[]) {
    const key = `${id}:${keyID ?? ''}:${ids.join(',')}`;
    if (key === lastKey) return;
    lastKey = key;
    await refresh();
  }

  async function refresh() {
    if (!userID || !activeKeyID) return;
    reconciled = false;
    // Nothing renders until every id has been fetched and verified, so a
    // mark never appears from evidence this device has not checked.
    const ok = await reconcileVouches(userID, vouchIDs);
    live = await liveVouchesFor(userID, activeKeyID);
    stale = await staleVouchesFor(userID, activeKeyID);
    rootIDs = await trustRootsRepository.activeIDs();
    reconciled = ok;
  }

  $: ownVouch = me ? live.find((v) => v.voucherUserID === me) : undefined;
  $: fromRoots = live.filter((v) => v.voucherUserID !== me && rootIDs.has(v.voucherUserID));
  $: fromOthers = live.filter((v) => v.voucherUserID !== me && !rootIDs.has(v.voucherUserID));

  async function withdraw(vouch: VouchRecord) {
    withdrawing = vouch.id;
    try {
      await withdrawVouch(vouch.subjectUserID, vouch.subjectKeyID);
      notificationStore.success('Verification withdrawn');
      await refresh();
    } catch (error) {
      console.error('[trust] withdraw failed', error);
      notificationStore.error('Could not withdraw');
    } finally {
      withdrawing = '';
    }
  }

  function formatDate(iso: string): string {
    return new Date(iso).toLocaleDateString();
  }
</script>

{#if reconciled && (live.length > 0 || stale.length > 0)}
  <section class="trust">
    <h3>Verification</h3>

    {#if ownVouch}
      <p class="row own">
        You verified this key on {formatDate(ownVouch.serverSignature.timestamp)}.
        <button
          class="link-btn"
          disabled={withdrawing === ownVouch.id}
          on:click={() => withdraw(ownVouch)}
        >
          {withdrawing === ownVouch.id ? 'Withdrawing…' : 'Withdraw'}
        </button>
      </p>
    {/if}

    {#if fromRoots.length > 0}
      <p class="row">
        Verified by people you verified:
        {#each fromRoots as vouch, i (vouch.id)}
          <Username userID={vouch.voucherUserID} at={true} />{i < fromRoots.length - 1 ? ', ' : ''}
        {/each}
      </p>
    {/if}

    {#if fromOthers.length > 0}
      <p class="row muted">
        Also verified by {fromOthers.length}
        {fromOthers.length === 1 ? 'person' : 'people'} you have not verified.
      </p>
    {/if}

    {#if stale.length > 0}
      <p class="row stale">
        Previously verified on an older key by {stale.length}
        {stale.length === 1 ? 'person' : 'people'}. That does not apply to the
        key in use now.
      </p>
    {/if}
  </section>
{/if}

<style>
  .trust {
    border-top: 1px solid var(--border);
    margin-top: 1rem;
    padding-top: 0.75rem;
  }

  h3 {
    font-size: 0.9rem;
    margin: 0 0 0.5rem;
  }

  .row {
    font-size: 0.85rem;
    line-height: 1.5;
    margin: 0 0 0.4rem;
  }

  .row.muted,
  .row.stale {
    color: var(--muted);
  }

  .row.stale {
    font-size: 0.8rem;
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

  .link-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
