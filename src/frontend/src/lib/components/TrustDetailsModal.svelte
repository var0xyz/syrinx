<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import Username from '$lib/components/Username.svelte';
  import ServerName from '$lib/components/ServerName.svelte';
  import TrustMark from '$lib/components/TrustMark.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import { foreignServerOf, unreachableServerMessage } from '$lib/services/peerServers';
  import { withdrawVouch, type KeyChangeKind } from '$lib/services/vouches';
  import WithdrawVouchButton from '$lib/components/WithdrawVouchButton.svelte';
  import type { VouchRecord } from '$lib/repositories/vouches';

  export let open = false;
  export let userID: string;
  /** Verified vouches naming the subject's current key. */
  export let live: VouchRecord[] = [];
  /** Vouches on a key the subject has since replaced. */
  export let stale: VouchRecord[] = [];
  export let keyChange: KeyChangeKind | null = null;

  const dispatch = createEventDispatcher();

  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;

  let withdrawing = '';

  // Someone else's vouch is a claim about who this person is, and that
  // does not lapse when they rotate a key. Only the key binding does, and
  // only the verifier can act on it.
  $: byOthers = newestPerVoucher([...live, ...stale].filter((v) => v.voucherUserId !== me));

  /** One row per person: re-vouching is allowed, and only the latest counts. */
  function newestPerVoucher(vouches: VouchRecord[]): VouchRecord[] {
    const newest = new Map<string, VouchRecord>();
    for (const vouch of vouches) {
      const held = newest.get(vouch.voucherUserId);
      if (!held || vouch.serverSignature.signedAt > held.serverSignature.signedAt) {
        newest.set(vouch.voucherUserId, vouch);
      }
    }
    return [...newest.values()];
  }

  $: ownVouch = me ? live.find((v) => v.voucherUserId === me) : undefined;

  // Only your own lapsed verification is actionable, and only while you
  // have not re-verified: once you have, the old one says nothing.
  $: ownStale =
    me && !ownVouch
      ? stale
          .filter((v) => v.voucherUserId === me)
          .sort((a, b) =>
            (b.serverSignature?.signedAt ?? 0) - (a.serverSignature?.signedAt ?? 0)
          )[0]
      : undefined;

  function close() {
    dispatch('close');
  }

  async function withdraw(vouch: VouchRecord) {
    withdrawing = vouch.id;
    try {
      await withdrawVouch(vouch.id, vouch.subjectUserId, vouch.subjectKeyId);
      notificationStore.success('Verification withdrawn');
      dispatch('changed');
    } catch (error) {
      console.error('[trust] withdraw failed', error);
      notificationStore.error(
        foreignServerOf(vouch.subjectUserId)
          ? await unreachableServerMessage(vouch.subjectUserId)
          : 'Could not withdraw'
      );
    } finally {
      withdrawing = '';
    }
  }

  function formatDate(unix: number): string {
    return new Date(unix * 1000).toLocaleDateString();
  }
</script>

{#if open}
  <div
    class="modal-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="trust-modal-title"
    tabindex="-1"
    on:click={(e) => e.target === e.currentTarget && close()}
    on:keydown={(e) => e.key === 'Escape' && close()}
  >
    <div class="modal">
      <h2 id="trust-modal-title">Verified by</h2>

      {#if keyChange === 'unexplained'}
        <p class="row alarm">
          This account’s key changed and the change is not signed by the
          previous key. Do not treat this account as verified.
        </p>
      {:else if keyChange === 'rotation'}
        <p class="row muted">
          They rotated their key. Your previous verification no longer applies —
          verify again next time you see them.
        </p>
      {:else if keyChange === 'revocation'}
        <p class="row muted">
          The key you verified was revoked, so your verification no longer
          applies to the key in use now.
        </p>
      {:else if keyChange === 'vouched-key-revoked'}
        <p class="row muted">
          The key you verified is still the one in use, but it has been
          revoked. Your verification no longer rules out someone intercepting
          this account.
        </p>
      {/if}

      {#if ownVouch}
        <div class="row own">
          <p class="own-line">
            You verified this user on {formatDate(ownVouch.serverSignature.signedAt)}.
          </p>
          {#if ownVouch.note}
            <p class="note">“{ownVouch.note}”</p>
          {/if}
          <WithdrawVouchButton
            subjectUserID={userID}
            busy={withdrawing === ownVouch.id}
            on:withdraw={() => withdraw(ownVouch)}
          />
        </div>
      {/if}

      {#if byOthers.length > 0}
        <section class="group">
          <ul class="vouchers">
            {#each byOthers as vouch (vouch.id)}
              <li>
                <span class="voucher" on:click={close} role="presentation">
                  <Username userID={vouch.voucherUserId} at={true} /><ServerName userID={vouch.voucherUserId} />
                </span>
                <TrustMark userID={vouch.voucherUserId} linked={false} />
                <span class="when">{formatDate(vouch.serverSignature.signedAt)}</span>
                {#if vouch.note}
                  <span class="note">“{vouch.note}”</span>
                {/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      {#if ownStale}
        <section class="group stale">
          <h3>Their key changed since you verified</h3>
          <p class="hint">
            You verified them on {formatDate(ownStale.serverSignature.signedAt)}.
            That still stands — but you are no longer holding them to a
            key. Verify again next time you see them.
          </p>
        </section>
      {/if}

      {#if byOthers.length === 0 && !ownVouch && !ownStale && !keyChange}
        <p class="row muted">
          Nobody has verified this account yet.
        </p>
      {/if}

      <button class="btn primary" on:click={close}>Done</button>

      <button class="explain-btn" on:click={() => dispatch('explain')}>
        What is this?
        <span class="info-icon" aria-hidden="true"></span>
      </button>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    /* Above pages' floating action buttons (z-index: 1000) so the modal
       covers them instead of the button floating over the dialog. */
    z-index: 1100;
    padding: 1rem;
  }

  .modal {
    background: var(--bg, #fff);
    border-radius: 8px;
    padding: 1.25rem;
    max-width: 28rem;
    width: 100%;
    max-height: 85vh;
    overflow-y: auto;
  }

  h2 {
    font-size: 1rem;
    margin: 0 0 0.75rem;
  }

  .row {
    font-size: 0.85rem;
    line-height: 1.5;
    margin: 0 0 0.5rem;
  }

  .row.muted {
    color: var(--muted);
  }

  .row.alarm {
    color: var(--error, #e03131);
    font-weight: 500;
  }

  .row.own {
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.6rem 0.75rem;
  }

  .own-line {
    margin: 0 0 0.35rem;
  }

  .group {
    margin-bottom: 0.75rem;
  }

  .group h3 {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
    margin: 0 0 0.35rem;
  }

  .group.stale h3 {
    opacity: 0.75;
  }

  .hint {
    font-size: 0.75rem;
    color: var(--muted);
    margin: 0 0 0.35rem;
  }

  .vouchers {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 0.85rem;
  }

  /* The username navigates away, so the dialog must not stay open over
     the page it lands on. */
  .voucher {
    display: inline-flex;
    min-width: 0;
  }

  .vouchers li {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.4rem;
    padding-top: 0.25rem;
  }

  .vouchers li:first-child {
    padding: 0;
  }

  .when {
    font-size: 0.75rem;
    color: var(--muted);
  }

  .note {
    font-size: 0.8rem;
    color: var(--muted);
    font-style: italic;
    margin: 0;
    flex-basis: 100%;
    overflow-wrap: anywhere;
  }

  .btn {
    padding: 0.4rem 1rem;
    border: none;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .btn.primary {
    background: var(--accent, #1971c2);
    color: #fff;
  }

  .explain-btn {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    margin: 0.75rem auto 0;
    padding: 0;
    background: none;
    border: none;
    font-size: 0.8rem;
    color: var(--muted);
    cursor: pointer;
  }

  .explain-btn:hover {
    color: var(--fg);
  }

  .info-icon {
    display: inline-block;
    width: 0.9rem;
    height: 0.9rem;
    background-color: currentColor;
    -webkit-mask-image: url('/icons/info-16.svg');
    mask-image: url('/icons/info-16.svg');
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
  }
</style>
