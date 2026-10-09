<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import BottomToolbar from '$lib/components/BottomToolbar.svelte';
  import Username from '$lib/components/Username.svelte';
  import ServerName from '$lib/components/ServerName.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { foreignServerOf, unreachableServerMessage } from '$lib/services/peerServers';
  import {
    acceptVouch,
    auditStateFor,
    declineVouch,
    myDeclinedVouches,
    myVouches,
    unreviewedVouches,
    withdrawVouch,
    type AuditState,
  } from '$lib/services/vouches';
  import WithdrawVouchButton from '$lib/components/WithdrawVouchButton.svelte';
  import type * as api from '$lib/types/api';

  let vouches: api.Vouch[] = [];
  let loading = true;
  let failed = false;
  let withdrawing = new Set<string>();
  // On the server but neither accepted nor declined on this device.
  let unreviewed: api.Vouch[] = [];
  let declined: api.Vouch[] = [];
  let reviewing = new Set<string>();

  onMount(load);

  /** Local only: a server response cannot be shown to be complete, and
   * this list's whole question is whether anything is missing. */
  async function load() {
    loading = true;
    failed = false;
    try {
      vouches = await myVouches();
      declined = await myDeclinedVouches();
    } catch (error) {
      console.error('[audit] could not load vouches', error);
      failed = true;
    } finally {
      loading = false;
    }
    void checkServer();
  }

  /** Background and silent: the local list stands on its own. */
  async function checkServer() {
    try {
      unreviewed = await unreviewedVouches();
    } catch (error) {
      console.error('[audit] could not check the server', error);
    }
  }

  async function review(list: api.Vouch[], accept: boolean) {
    const ids = new Set(list.map((v) => v.id));
    reviewing = new Set([...reviewing, ...ids]);
    try {
      for (const vouch of list) {
        if (accept) await acceptVouch(vouch);
        else await declineVouch(vouch);
        unreviewed = unreviewed.filter((v) => v.id !== vouch.id);
      }
    } catch (error) {
      console.error('[audit] review failed', error);
      notificationStore.error(accept ? 'Could not accept' : 'Could not decline');
    } finally {
      reviewing = new Set([...reviewing].filter((id) => !ids.has(id)));
      vouches = await myVouches();
      declined = await myDeclinedVouches();
    }
  }

  /** Grouped by the key that signed, the natural unit of "everything I
   * signed while that key was live". */
  $: groups = groupByKey(vouches);

  function groupByKey(list: api.Vouch[]): [string, api.Vouch[]][] {
    const byKey = new Map<string, api.Vouch[]>();
    for (const vouch of list) {
      const held = byKey.get(vouch.voucherKeyId);
      if (held) held.push(vouch);
      else byKey.set(vouch.voucherKeyId, [vouch]);
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
      await withdrawVouch(vouch.id, vouch.subjectUserId, vouch.subjectKeyId);
      notificationStore.success('Verification withdrawn');
    } catch (error) {
      console.error('[audit] withdraw failed', vouch.id, error);
      notificationStore.error(
        foreignServerOf(vouch.subjectUserId)
          ? await unreachableServerMessage(vouch.subjectUserId)
          : 'Could not withdraw'
      );
    } finally {
      withdrawing = new Set([...withdrawing].filter((w) => w !== vouch.id));
    }
  }

  function formatWhen(unix: number): string {
    return new Date(unix * 1000).toLocaleString();
  }
</script>

<Auth>
<SideNav currentPage="account" />
<div class="page">
<div class="audit">
  <h1>Keys you verified</h1>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if failed}
    <p class="muted">Could not load your verifications.</p>
    <button class="btn secondary" on:click={load}>Try again</button>
  {:else}
    {#if unreviewed.length > 0}
      <section class="review">
        <h2>Found on the server</h2>
        <p class="muted">These verifications were made with your account but
          aren't on this device. Accept the ones you made and decline any you
          don't recognise.</p>
        {#if unreviewed.length > 1}
          <div class="actions">
            <button class="btn" on:click={() => review(unreviewed, false)} disabled={reviewing.size > 0}>Decline all</button>
            <button class="btn primary" on:click={() => review(unreviewed, true)} disabled={reviewing.size > 0}>Accept all</button>
          </div>
        {/if}
        <ul>
          {#each unreviewed as vouch (vouch.id)}
            <li>
              {@render detail(vouch)}
              <div class="item-actions">
                <button class="btn" on:click={() => review([vouch], false)} disabled={reviewing.has(vouch.id)}>Decline</button>
                <button class="btn primary" on:click={() => review([vouch], true)} disabled={reviewing.has(vouch.id)}>Accept</button>
              </div>
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if vouches.length === 0}
      <p class="muted">You have not verified anyone yet.</p>
    {/if}

    {#each groups as [keyID, group] (keyID)}
      <section class="group">
        <h2>Signed with <code>{keyID}</code></h2>
        <ul>
          {#each group as vouch (vouch.id)}
            <li class:withdrawn={!!vouch.withdrawal}>
              <div class="detail">
                <span class="who"><Username userID={vouch.subjectUserId} at={true} /><ServerName userID={vouch.subjectUserId} /></span>
                <span class="state {stateOf(states, vouch)}">{stateOf(states, vouch)}</span>
                <span class="when">{formatWhen(vouch.serverSignature.signedAt)}</span>
                <code class="key">{vouch.subjectKeyId}</code>
                {#if vouch.note}
                  <span class="note">“{vouch.note}”</span>
                {/if}
              </div>
              {#if !vouch.withdrawal}
                <WithdrawVouchButton
                  subjectUserID={vouch.subjectUserId}
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

    {#if declined.length > 0}
      <section class="group">
        <h2>Declined</h2>
        <p class="muted">You said you didn't make these. The server can't delete
          them, so they stay listed here.</p>
        <ul>
          {#each declined as vouch (vouch.id)}
            <li class="declined">
              {@render detail(vouch)}
              <div class="item-actions">
                <button class="btn" on:click={() => review([vouch], true)} disabled={reviewing.has(vouch.id)}>Accept</button>
              </div>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
</div>
<BottomToolbar currentPage="account" />
</div>
</Auth>

{#snippet detail(vouch: api.Vouch)}
  <div class="detail">
    <span class="who"><Username userID={vouch.subjectUserId} at={true} /><ServerName userID={vouch.subjectUserId} /></span>
    <span class="when">{formatWhen(vouch.serverSignature.signedAt)}</span>
    <code class="key">{vouch.subjectKeyId}</code>
    {#if vouch.note}
      <span class="note">“{vouch.note}”</span>
    {/if}
  </div>
{/snippet}

<style>
  .page {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  @media (min-width: 768px) {
    .page {
      padding-left: var(--sidenav-width);
    }
  }

  @media (min-width: 1400px) {
    .page {
      padding-right: var(--activity-sidebar-width);
    }
  }

  .audit {
    flex: 1;
    width: 100%;
    max-width: 44rem;
    margin: 0 auto;
    padding: 1.5rem 1rem 4rem;
  }

  h1 {
    font-size: 1.35rem;
    margin: 0 0 0.5rem;
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

  .actions {
    display: flex;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .review {
    margin-bottom: 1.5rem;
  }

  .review h2 {
    font-size: 1rem;
    margin: 0 0 0.25rem;
  }

  .item-actions {
    display: flex;
    gap: 0.4rem;
  }

  .item-actions .btn {
    width: auto;
  }

  li.declined .detail {
    text-decoration: line-through;
    opacity: 0.55;
  }

  .btn.primary {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--button-text);
    font-weight: 600;
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
