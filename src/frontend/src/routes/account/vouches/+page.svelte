<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Username from '$lib/components/Username.svelte';
  import ServerName from '$lib/components/ServerName.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { foreignServerOf, unreachableServerMessage } from '$lib/services/peerServers';
  import {
    auditStateFor,
    myVouches,
    recoverMyVouches,
    withdrawVouch,
    type AuditState,
  } from '$lib/services/vouches';
  import WithdrawVouchButton from '$lib/components/WithdrawVouchButton.svelte';
  import type * as api from '$lib/types/api';

  let vouches: api.Vouch[] = [];
  let loading = true;
  let failed = false;
  let withdrawing = new Set<string>();
  let recovering = false;
  let recovered = false;

  onMount(load);

  /** Local only: a server response cannot be shown to be complete, and
   * this list's whole question is whether anything is missing. */
  async function load() {
    loading = true;
    failed = false;
    try {
      vouches = await myVouches();
    } catch (error) {
      console.error('[audit] could not load vouches', error);
      failed = true;
    } finally {
      loading = false;
    }
  }

  /** Degraded restore: certs are verified on the way in, but a row the
   * server withheld stays missing. */
  async function recover() {
    recovering = true;
    try {
      await recoverMyVouches();
      vouches = await myVouches();
      recovered = true;
      notificationStore.success('Restored what the server still holds');
    } catch (error) {
      console.error('[audit] recovery failed', error);
      notificationStore.error('Could not restore from the server');
    } finally {
      recovering = false;
    }
  }

  /** Grouped by the key that signed, the natural unit of "everything I
   * signed while that key was live". */
  $: groups = groupByKey(vouches);

  function groupByKey(list: api.Vouch[]): [string, api.Vouch[]][] {
    const byKey = new Map<string, api.Vouch[]>();
    for (const vouch of list) {
      const held = byKey.get(vouch.voucherKeyID);
      if (held) held.push(vouch);
      else byKey.set(vouch.voucherKeyID, [vouch]);
    }
    return [...byKey];
  }

  /** Computed locally, never read from the server's `stale` hint. */
  let states = new Map<string, AuditState>();

  $: void computeStates(vouches);

  async function computeStates(list: api.Vouch[]) {
    const next = new Map<string, AuditState>();
    for (const vouch of list) {
      next.set(vouch.id, await auditStateFor(vouch));
    }
    states = next;
  }

  /** Takes the map so the template re-renders when it is replaced. */
  function stateOf(map: Map<string, AuditState>, vouch: api.Vouch): AuditState {
    return map.get(vouch.id) ?? 'live';
  }

  async function withdrawOne(vouch: api.Vouch) {
    withdrawing = new Set([...withdrawing, vouch.id]);
    try {
      await withdrawVouch(vouch.id, vouch.subjectUserID, vouch.subjectKeyID);
      notificationStore.success('Verification withdrawn');
    } catch (error) {
      console.error('[audit] withdraw failed', vouch.id, error);
      notificationStore.error(
        foreignServerOf(vouch.subjectUserID)
          ? await unreachableServerMessage(vouch.subjectUserID)
          : 'Could not withdraw'
      );
    } finally {
      withdrawing = new Set([...withdrawing].filter((w) => w !== vouch.id));
    }
  }

  function formatWhen(iso: string): string {
    return new Date(iso).toLocaleString();
  }
</script>

<Auth>
<SideNav currentPage="" />
<div class="audit">
  <h1>Keys you verified</h1>
  <p class="lead">Every verification you have made, newest first. If you see
    one you do not remember making, withdraw it.</p>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if failed}
    <p class="muted">Could not load your verifications.</p>
    <button class="btn secondary" on:click={load}>Try again</button>
  {:else if vouches.length === 0}
    <p class="muted">You have not verified anyone yet.</p>
    {#if !recovered}
      <p class="recover-note">On a new device this list starts empty. You can
        ask the server for what it still holds, but it can leave things out,
        so treat the result as a starting point rather than your history.</p>
      <button class="btn secondary" on:click={recover} disabled={recovering}>
        {recovering ? 'Restoring…' : 'Restore from server'}
      </button>
    {/if}
  {:else}
    {#each groups as [keyID, group] (keyID)}
      <section class="group">
        <h2>Signed with <code>{keyID}</code></h2>
        <ul>
          {#each group as vouch (vouch.id)}
            <li class:withdrawn={!!vouch.withdrawal}>
              <div class="detail">
                <span class="who"><Username userID={vouch.subjectUserID} at={true} /><ServerName userID={vouch.subjectUserID} /></span>
                <span class="state {stateOf(states, vouch)}">{stateOf(states, vouch)}</span>
                <span class="when">{formatWhen(vouch.serverSignature.timestamp)}</span>
                <code class="key">{vouch.subjectKeyID}</code>
                {#if vouch.note}
                  <span class="note">“{vouch.note}”</span>
                {/if}
              </div>
              {#if !vouch.withdrawal}
                <WithdrawVouchButton
                  subjectUserID={vouch.subjectUserID}
                  busy={withdrawing.has(vouch.id)}
                  compact={true}
                  on:withdraw={() => withdrawOne(vouch).then(load)}
                />
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {/each}
  {/if}
</div>
</Auth>

<style>
  .audit {
    max-width: 44rem;
    margin: 0 auto;
    padding: 1.5rem 1rem 4rem;
  }

  h1 {
    font-size: 1.35rem;
    margin: 0 0 0.5rem;
  }

  .lead {
    font-size: 0.9rem;
    color: var(--muted);
    line-height: 1.5;
    margin: 0 0 1.25rem;
  }

  .recover-note {
    font-size: 0.85rem;
    line-height: 1.5;
    color: var(--muted);
    max-width: 34rem;
  }

  .muted {
    color: var(--muted);
    font-size: 0.9rem;
  }

  .group {
    margin-bottom: 1.5rem;
  }

  .group h2 {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--muted);
    margin: 0 0 0.5rem;
  }

  .group h2 code {
    word-break: break-all;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  /* Grid, not flex: the withdraw control is a child component, so its
     width cannot be constrained from here. 1fr auto gives the text the
     leftover space instead of letting the button bid for it. */
  li {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: 0.5rem;
    padding: 0.6rem 0;
    border-top: 1px solid var(--border);
    font-size: 0.85rem;
  }

  li.withdrawn {
    opacity: 0.55;
  }

  .detail {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
  }

  .state {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }

  .when,
  .key,
  .note {
    font-size: 0.75rem;
    color: var(--muted);
    word-break: break-all;
  }

  .btn {
    padding: 0.35rem 0.7rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    cursor: pointer;
    font-size: 0.8rem;
  }

  .btn.danger {
    background: var(--error, #e03131);
    border-color: var(--error, #e03131);
    color: #fff;
    font-weight: 600;
  }

  .link-btn {
    background: none;
    border: none;
    padding: 0;
    font-size: 0.8rem;
    color: var(--accent, #1971c2);
    cursor: pointer;
    text-decoration: underline;
    white-space: nowrap;
  }

  .link-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
