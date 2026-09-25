<script lang="ts">
  import { onMount } from 'svelte';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Username from '$lib/components/Username.svelte';
  import { apiService } from '$lib/services/api';
  import { notificationStore } from '$lib/stores/notifications';
  import { withdrawVouch } from '$lib/services/vouches';
  import type * as api from '$lib/types/api';

  let vouches: api.Vouch[] = [];
  let loading = true;
  let failed = false;
  let withdrawing = new Set<string>();
  let selected = new Set<string>();

  onMount(load);

  async function load() {
    loading = true;
    failed = false;
    try {
      // Server-ordered by countersignature time: the one timestamp in the
      // record this device did not choose, so a burst cannot be backdated.
      const page = await apiService.getMyVouches();
      vouches = page.vouches ?? [];
    } catch (error) {
      console.error('[audit] could not load vouches', error);
      failed = true;
    } finally {
      loading = false;
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

  function stateOf(vouch: api.Vouch): string {
    if (vouch.withdrawn) return 'withdrawn';
    return vouch.stale ? 'previous key' : 'live';
  }

  function toggle(id: string) {
    selected = new Set(
      selected.has(id) ? [...selected].filter((s) => s !== id) : [...selected, id]
    );
  }

  async function withdrawOne(vouch: api.Vouch) {
    withdrawing = new Set([...withdrawing, vouch.id]);
    try {
      await withdrawVouch(vouch.subjectUserID, vouch.subjectKeyID);
      notificationStore.success('Verification withdrawn');
    } catch (error) {
      console.error('[audit] withdraw failed', vouch.id, error);
      notificationStore.error('Could not withdraw');
    } finally {
      withdrawing = new Set([...withdrawing].filter((w) => w !== vouch.id));
    }
  }

  /** One withdrawal cert per vouch; there is no bulk signature. */
  async function withdrawSelected() {
    const targets = vouches.filter((v) => selected.has(v.id) && !v.withdrawn);
    for (const vouch of targets) {
      await withdrawOne(vouch);
    }
    selected = new Set();
    await load();
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
  {:else}
    {#if selected.size > 0}
      <div class="bulk">
        <span>{selected.size} selected</span>
        <button class="btn danger" on:click={withdrawSelected}>Withdraw selected</button>
        <button class="btn secondary" on:click={() => (selected = new Set())}>Clear</button>
      </div>
    {/if}

    {#each groups as [keyID, group] (keyID)}
      <section class="group">
        <h2>Signed with <code>{keyID}</code></h2>
        <ul>
          {#each group as vouch (vouch.id)}
            <li class:withdrawn={vouch.withdrawn}>
              <label class="pick">
                <input
                  type="checkbox"
                  checked={selected.has(vouch.id)}
                  disabled={vouch.withdrawn}
                  on:change={() => toggle(vouch.id)}
                />
              </label>
              <div class="detail">
                <span class="who"><Username userID={vouch.subjectUserID} at={true} /></span>
                <span class="state {stateOf(vouch)}">{stateOf(vouch)}</span>
                <span class="when">{formatWhen(vouch.serverSignature.timestamp)}</span>
                <code class="key">{vouch.subjectKeyID}</code>
                {#if vouch.note}
                  <span class="note">“{vouch.note}”</span>
                {/if}
              </div>
              {#if !vouch.withdrawn}
                <button
                  class="link-btn"
                  disabled={withdrawing.has(vouch.id)}
                  on:click={() => withdrawOne(vouch).then(load)}
                >
                  {withdrawing.has(vouch.id) ? 'Withdrawing…' : 'Withdraw'}
                </button>
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

  .muted {
    color: var(--muted);
    font-size: 0.9rem;
  }

  .bulk {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    padding: 0.5rem 0.75rem;
    background: var(--input-bg);
    border-radius: 4px;
    margin-bottom: 1rem;
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

  li {
    display: flex;
    align-items: flex-start;
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
    flex: 1;
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
    color: var(--error, #e03131);
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
