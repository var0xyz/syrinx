<script lang="ts">
  import { countCharacters } from '$lib/utils/text';
  import { createEventDispatcher } from 'svelte';
  import { goto } from '$app/navigation';
  import Username from '$lib/components/Username.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import type { ReedType } from '$lib/types/reed';
  import { deleteFlagCase, dismissCase, syncAdminRecords } from '$lib/services/admin';
  import type { FlagCase } from '$lib/repositories/flags';
  import { parseCanonicalId } from '$lib/utils/identityRef';
  import { formatRelativeTime, fromUnix } from '$lib/utils/time';

  export let flagCase: FlagCase;
  /** The flagged reed: undefined or null while it's retrieved. */
  export let reed: ReedType | null | undefined = undefined;
  /** The viewer's server, to tell remote reporters apart. */
  export let serverID: string;
  /** Flags the author's reeds received in all, linking to their history. */
  export let authorFlagCount: number | undefined = undefined;
  /** Shows each flag's state, for an author's history. */
  export let showStates = false;

  const MAX_DISMISS_REASON = 140;
  const NOTES_PAGE = 10;
  // A closed case may be deleted six months after it closed.
  const RETENTION_SECONDS = 183 * 24 * 60 * 60;
  const dispatch = createEventDispatcher<{ takeAction: string }>();

  let dismissing = false;
  let dismissReason = '';
  let dismissError = '';
  let dismissSubmitting = false;
  let shownFlags = NOTES_PAGE;
  let confirmingDelete = false;
  let deleteError = '';
  let deleteSubmitting = false;

  // A case is open until dismissed or until every flag in it is withdrawn.
  $: open = flagCase.dismissal || flagCase.voidedAt ? [] : flagCase.flags.filter((f) => !f.withdrawal);
  $: closedAt = flagCase.dismissal?.serverSignature?.signedAt ?? flagCase.voidedAt ?? 0;
  $: deletable = closedAt > 0 && Date.now() / 1000 - closedAt > RETENTION_SECONDS;

  function when(signedAt: number | undefined): string {
    return formatRelativeTime(fromUnix(signedAt ?? 0) ?? new Date());
  }

  /** The server a remote reporter belongs to, null for a local one. */
  function remoteServer(userID: string): string | null {
    const server = parseCanonicalId(userID)?.[1];
    return server && server !== serverID ? server : null;
  }

  function openDismiss(): void {
    dismissing = true;
    dismissReason = '';
    dismissError = '';
  }

  async function submitDismiss(): Promise<void> {
    dismissSubmitting = true;
    dismissError = '';
    try {
      await dismissCase(flagCase.id, flagCase.reedId, dismissReason.trim());
      dismissing = false;
    } catch (err) {
      const message = err instanceof Error ? err.message : '';
      if (message === 'case-closed') {
        dismissError = 'This case was closed meanwhile.';
        void syncAdminRecords();
      } else if (message === 'opened-before-recovery') {
        dismissError = "This case came back with the server's recovery and can't be acted on until recovery is over.";
      } else {
        dismissError = message || 'Could not dismiss this case.';
      }
    } finally {
      dismissSubmitting = false;
    }
  }

  async function submitDelete(): Promise<void> {
    deleteSubmitting = true;
    deleteError = '';
    try {
      await deleteFlagCase(flagCase.id);
    } catch (err) {
      deleteError = err instanceof Error && err.message ? err.message : 'Could not delete this case.';
      deleteSubmitting = false;
    }
  }
</script>

<li class="flag">
  <div
    class="reed"
    role="link"
    tabindex="0"
    on:click={() => goto(`/reed/${flagCase.reedId}`)}
    on:keydown={(e) => e.key === 'Enter' && goto(`/reed/${flagCase.reedId}`)}
  >
    <div class="reed-head">
      <ReedAuthorHeader
        userID={flagCase.authorId}
        username=""
        subtext={reed?.serverSignature ? when(reed.serverSignature.signedAt) : ''}
        stopPropagation
      />
      {#if authorFlagCount !== undefined}
        <a class="history-link" href="/admin/flags/{flagCase.authorId}" on:click|stopPropagation>
          {authorFlagCount} {authorFlagCount === 1 ? 'flag' : 'flags'} on their reeds
        </a>
      {/if}
    </div>
    {#if reed}
      {#if (reed.content ?? '').trim()}
        <div class="reed-content"><MarkdownParser text={reed.content} preview={true} /></div>
      {/if}
    {:else}
      <p class="reed-content loading">Retrieving from the network…</p>
    {/if}
  </div>

  <div class="section">
    {#if flagCase.flags.length > NOTES_PAGE}
      <span class="report-count">{flagCase.flags.length} flags</span>
    {/if}
    <ul class="reports">
      {#each flagCase.flags.slice(0, shownFlags) as rep (rep.reporterId)}
        {@const server = remoteServer(rep.reporterId)}
        <li>
          <span class="name"><Username userID={rep.reporterId} color="var(--primary)" /></span>{#if server}<span class="server">({server})</span>{/if}
          <!-- A withdrawn flag's note isn't the admins' to read anymore. -->
          {#if rep.note && !rep.withdrawal}<span class="note">“{rep.note}”</span>{/if}
          <span class="report-when">{when(rep.serverSignature?.signedAt)}</span>
          {#if showStates}
            {#if rep.withdrawal}
              <span class="state">Withdrawn {when(rep.withdrawal.serverSignature?.signedAt)}</span>
            {:else if flagCase.dismissal}
              <span class="state">Dismissed</span>
            {:else}
              <span class="state open">Open</span>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
    {#if flagCase.flags.length > shownFlags}
      <button class="show-more" type="button" on:click={() => (shownFlags += NOTES_PAGE)}>
        Show {Math.min(NOTES_PAGE, flagCase.flags.length - shownFlags)} more
      </button>
    {/if}
  </div>

  {#if flagCase.dismissal}
    <div class="section">
      <blockquote class="reason">{flagCase.dismissal.reason}</blockquote>
      <p class="meta">
        Dismissed by
        {#if flagCase.dismissal.dismissedBy}
          <span class="name"><Username userID={flagCase.dismissal.dismissedBy} color="var(--primary)" /></span>
        {:else}
          a removed account
        {/if}
        · {when(flagCase.dismissal.serverSignature?.signedAt)}
      </p>
    </div>
  {:else if flagCase.voidedAt}
    <div class="section">
      <p class="meta">Void: every flag was withdrawn · {when(flagCase.voidedAt)}</p>
    </div>
  {/if}
  {#if !flagCase.dismissal && !flagCase.voidedAt && dismissing}
    <div class="section">
      <label class="dismiss-field">
        <span>What was done about these flags?</span>
        <textarea bind:value={dismissReason} rows="3" placeholder="The action you took, if any, or why none was needed"></textarea>
        <span class="character-counter">{countCharacters(dismissReason)}/{MAX_DISMISS_REASON} characters</span>
      </label>
      {#if dismissError}<p class="decision-error" role="alert">{dismissError}</p>{/if}
      <div class="actions">
        <button class="btn secondary" type="button" on:click={() => (dismissing = false)}>Cancel</button>
        <button
          class="btn primary"
          type="button"
          disabled={dismissSubmitting || !dismissReason.trim() || countCharacters(dismissReason) > MAX_DISMISS_REASON}
          on:click={submitDismiss}
        >
          Dismiss
        </button>
      </div>
    </div>
  {:else if open.length > 0}
    <div class="section actions">
      <button class="btn secondary" type="button" on:click={() => dispatch('takeAction', flagCase.authorId)}>
        Take action
      </button>
      <button class="btn secondary" type="button" on:click={openDismiss}>Dismiss</button>
    </div>
  {:else if deletable && confirmingDelete}
    <div class="section">
      <p class="meta">Delete this case and its flags? This can't be undone.</p>
      {#if deleteError}<p class="decision-error" role="alert">{deleteError}</p>{/if}
      <div class="actions">
        <button class="btn secondary" type="button" on:click={() => (confirmingDelete = false)}>Cancel</button>
        <button class="btn primary" type="button" disabled={deleteSubmitting} on:click={submitDelete}>Delete</button>
      </div>
    </div>
  {:else if deletable}
    <div class="section actions">
      <button class="btn secondary" type="button" on:click={() => (confirmingDelete = true)}>Delete</button>
    </div>
  {/if}
</li>

<style>
  .flag {
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    overflow: hidden;
    overflow-wrap: anywhere;
  }

  .reed {
    cursor: pointer;
  }

  .reed:hover {
    background: var(--input-bg);
  }

  .reed-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 1rem 1rem 0;
  }

  .history-link {
    flex-shrink: 0;
    font-size: 0.8rem;
    color: var(--muted);
  }

  .reed-content {
    margin: 0;
    padding: 0.75rem 1rem 1rem;
  }

  .reed-content.loading {
    color: var(--muted);
    font-style: italic;
    font-size: 0.9rem;
  }

  .section {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    border-top: 1px solid var(--border);
  }

  .report-count {
    font-size: 0.75rem;
    font-weight: 700;
    padding: 0.25rem 0.5rem;
    border-radius: 999px;
    background: var(--input-bg);
    color: #c0392b;
  }

  .reports {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .name {
    font-weight: 600;
  }

  .server {
    margin-left: 0.25rem;
  }

  .note {
    margin-left: 0.25rem;
    color: var(--fg);
  }

  .report-when,
  .state {
    margin-left: 0.5rem;
  }

  .state {
    font-weight: 600;
  }

  .state.open {
    color: #c0392b;
  }

  .show-more {
    width: auto;
    padding: 0;
    border: none;
    background: none;
    color: var(--primary);
    font-family: inherit;
    font-size: 0.85rem;
    font-weight: 600;
    cursor: pointer;
  }

  .reason {
    margin: 0;
    padding: 0.25rem 0.75rem;
    border-left: 3px solid var(--border);
    color: var(--fg);
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .meta {
    margin: 0;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .actions {
    display: flex;
    gap: 0.5rem;
    flex-direction: row;
    flex-wrap: wrap;
    align-self: stretch;
    justify-content: flex-end;
  }

  .section.actions {
    align-items: center;
  }

  .dismiss-field {
    align-self: stretch;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .dismiss-field textarea {
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--input-bg);
    color: var(--fg);
    font-family: inherit;
    font-size: 0.9rem;
    resize: vertical;
  }

  .character-counter {
    text-align: right;
    font-size: 0.8rem;
  }

  .decision-error {
    margin: 0;
    color: var(--error);
    font-size: 0.85rem;
  }

  .btn {
    width: auto;
    border: none;
    border-radius: 8px;
    padding: 0.5rem 1rem;
    font-family: inherit;
    font-size: 0.9rem;
    font-weight: 600;
    line-height: 1.25;
    cursor: pointer;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn.primary {
    background: var(--primary);
    color: var(--button-text);
  }

  .btn.secondary {
    background: transparent;
    color: var(--fg);
    border: 1px solid var(--border);
  }
</style>
