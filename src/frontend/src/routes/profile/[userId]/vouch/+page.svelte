<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import Auth from '$lib/components/Auth.svelte';
  import SideNav from '$lib/components/SideNav.svelte';
  import Username from '$lib/components/Username.svelte';
  import { notificationStore } from '$lib/stores/notifications';
  import {
    compareScannedKey,
    parseVouchFragment,
    type CompareResult,
  } from '$lib/services/vouchVerify';
  import { createVouch } from '$lib/services/vouches';
  import { MAX_VOUCH_NOTE_CHARS } from '$lib/utils/vouchNote';
  import { pendingVouchesRepository } from '$lib/repositories/pendingVouches';

  /** @type {import('./$types').PageData} */
  export let data;

  let scannedKeyID: string | null = null;
  let result: CompareResult | null = null;
  let comparing = true;
  let submitting = false;
  let done = false;
  let queued = false;
  let note = '';

  onMount(async () => {
    scannedKeyID = parseVouchFragment(window.location.hash, data.subjectUserID);
    if (!scannedKeyID) {
      result = {
        outcome: 'unresolvable',
        scannedKeyID: '',
        servedKeyID: null,
        reason: 'This link does not point to a valid key.',
      };
      comparing = false;
      return;
    }
    result = await compareScannedKey(data.subjectUserID, scannedKeyID);
    comparing = false;
  });

  async function confirm() {
    if (!scannedKeyID || result?.outcome !== 'same') return;
    submitting = true;
    try {
      // The scanned key is what gets attested, not the served one.
      await createVouch(data.subjectUserID, scannedKeyID, note.trim());
      done = true;
      notificationStore.success('You verified this key');
    } catch (error) {
      console.error('[vouch] could not submit', error);
      // The signature is queued before the request, so an unreachable
      // server delays the vouch rather than losing it.
      if (await pendingVouchesRepository.get(scannedKeyID)) {
        done = true;
        queued = true;
        notificationStore.success('Verification saved, will sync when online');
      } else {
        notificationStore.error('Could not save your verification');
      }
    } finally {
      submitting = false;
    }
  }
</script>

<Auth>
<SideNav currentPage="" />
<div class="vouch-container">
  <h1>Verify in person</h1>

  {#if data.isSelf}
    <p class="lead">This is your own code. Show it to someone else so they can
      check the key this app reports for you.</p>
    <a class="btn secondary" href={`/profile/${data.subjectUserID}`}>Back to profile</a>
  {:else if comparing}
    <p class="lead">Checking the key for
      <Username userID={data.subjectUserID} />…</p>
  {:else if done}
    <p class="lead success">You verified
      <Username userID={data.subjectUserID} />’s key.</p>
    {#if queued}
      <p class="detail">It will be published when this device is back online.</p>
    {/if}
    <p class="detail">Ask them to scan your code too, so you are verified on
      their device as well.</p>
    <a class="btn primary" href={`/profile/${data.subjectUserID}`}>Done</a>
  {:else if result?.outcome === 'same'}
    <p class="lead">The key
      <Username userID={data.subjectUserID} />
      showed you matches the key this app was given for them.</p>
    <p class="detail">Confirming records that <strong>you compared these in
      person</strong>. It does not mean you vouch for who they claim to be.</p>
    <code class="key-id">{scannedKeyID}</code>

    <label class="note-label" for="vouch-note">Note (optional, public)</label>
    <input
      id="vouch-note"
      class="note-input"
      type="text"
      bind:value={note}
      maxlength={MAX_VOUCH_NOTE_CHARS}
      placeholder="Met at…"
    />
    <span class="note-count">{note.length}/{MAX_VOUCH_NOTE_CHARS}</span>

    <div class="actions">
      <button class="btn primary" disabled={submitting} on:click={confirm}>
        {submitting ? 'Saving…' : 'Confirm verification'}
      </button>
      <button class="btn secondary" on:click={() => goto(`/profile/${data.subjectUserID}`)}>
        Cancel
      </button>
    </div>
  {:else if result?.outcome === 'differs'}
    <div class="alarm">
      <h2>The keys do not match</h2>
      <p>The key this app was given for
        <Username userID={data.subjectUserID} />
        is not the key they showed you.</p>
      <p class="detail">If they just changed keys, ask them to reopen their code
        and try again. Otherwise treat this account as compromised on this
        device, and do not send them anything private.</p>
      <dl class="compare">
        <dt>They showed</dt>
        <dd><code>{result.scannedKeyID}</code></dd>
        <dt>This app was given</dt>
        <dd><code>{result.servedKeyID}</code></dd>
      </dl>
      <p class="detail">Verification is not recorded when the keys disagree.</p>
    </div>
    <a class="btn secondary" href={`/profile/${data.subjectUserID}`}>Back to profile</a>
  {:else}
    <p class="lead">Could not check this key.</p>
    <p class="detail">{result?.reason ?? 'Try again when you are online.'}</p>
    <a class="btn secondary" href={`/profile/${data.subjectUserID}`}>Back to profile</a>
  {/if}
</div>
</Auth>

<style>
  .vouch-container {
    max-width: 34rem;
    margin: 0 auto;
    padding: 1.5rem 1rem 4rem;
  }

  h1 {
    font-size: 1.35rem;
    margin: 0 0 1rem;
  }

  .lead {
    font-size: 1rem;
    line-height: 1.5;
    margin: 0 0 0.75rem;
  }

  .lead.success {
    color: var(--success, #2f9e44);
  }

  .detail {
    font-size: 0.875rem;
    color: var(--muted);
    line-height: 1.5;
    margin: 0 0 1rem;
  }

  .key-id {
    display: block;
    font-size: 0.8rem;
    word-break: break-all;
    background: var(--input-bg);
    padding: 0.5rem;
    border-radius: 4px;
    margin-bottom: 1rem;
  }

  .note-label {
    display: block;
    font-size: 0.85rem;
    margin-bottom: 0.25rem;
  }

  .note-input {
    width: 100%;
    padding: 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
  }

  .note-count {
    display: block;
    text-align: right;
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 1rem;
  }

  .actions {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
  }

  .alarm {
    border: 1px solid var(--error, #e03131);
    border-radius: 6px;
    padding: 1rem;
    margin-bottom: 1rem;
  }

  .alarm h2 {
    color: var(--error, #e03131);
    font-size: 1.1rem;
    margin: 0 0 0.5rem;
  }

  .compare {
    margin: 1rem 0;
    font-size: 0.8rem;
  }

  .compare dt {
    color: var(--muted);
    margin-top: 0.5rem;
  }

  .compare dd {
    margin: 0.25rem 0 0;
  }

  .compare code {
    word-break: break-all;
  }

  .btn {
    display: inline-block;
    padding: 0.5rem 1rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--input-bg);
    color: var(--fg);
    cursor: pointer;
    text-decoration: none;
    font-size: 0.9rem;
  }

  .btn.primary {
    background: var(--accent, #1971c2);
    border-color: var(--accent, #1971c2);
    color: #fff;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
