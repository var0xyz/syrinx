<script>
  import { goto } from '$app/navigation';
  import { get } from 'svelte/store';
  import { reedsService } from '$lib/repositories/reeds';
  import { removedReedsRepository } from '$lib/repositories/removedReeds';
  import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
  import { userRepository } from '$lib/repositories/user';
  import { apiService } from '$lib/services/api';
  import { isOnline } from '$lib/services/pwa';
  import { verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import { formatRelativeTime } from '$lib/utils/time';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { resolveBlankEchoChain } from '$lib/utils/emptyEcho';
  import MarkdownParser from './MarkdownParser.svelte';

  /** @type {import('$lib/types/reed').ReedType | null | undefined} */
  export let reed = null;

  /** When `reed` is null, fetch by this `userID@serverID/reedID` ref. */
  export let reedRef = '';

  /** @type {'echo' | 'reply'} */
  export let type = 'echo';

  /** When true and no `reedRef`, show unavailable (invalid ref). */
  export let missing = false;

  /** @type {boolean} */
  export let linked = false;

  /** Optional preview line clamp (composer). */
  export let maxLines = 0;

  let username = '';
  let loading = true;
  /** @type {import('$lib/types/reed').ReedType | null} */
  let displayReed = null;
  let loadFailed = false;
  /** @type {{ userID: string; reedID: string; kind: 'reed' | 'account'; timestamp?: string } | null} */
  let removedTarget = null;

  $: icon = type === 'echo' ? '📢' : '💬';
  $: label = type === 'reply' ? 'Replying to ' : '';
  $: borderColor = type === 'echo' ? 'var(--primary)' : '#7c3aed';
  $: reedId = reed?.id ?? '';
  $: void load(reedId, reedRef, missing);

  async function resolveGone(authorId, targetReedId) {
    const accountCert = await removedAccountsRepository.get(authorId);
    if (accountCert) {
      return {
        userID: authorId,
        reedID: targetReedId,
        kind: 'account',
        timestamp: accountCert.serverSignature?.timestamp,
      };
    }

    const reedCert = await removedReedsRepository.get(targetReedId);
    if (reedCert && reedCert.userID === authorId) {
      return {
        userID: reedCert.userID,
        reedID: reedCert.reedID,
        kind: 'reed',
        timestamp: reedCert.serverSignature?.timestamp,
      };
    }

    if (!get(isOnline)) return null;
    try {
      const result = await apiService.getReedOrRemoval(authorId, targetReedId);
      if (result.kind !== 'gone' || !result.removal) return null;
      if (result.removal.type === 'account') {
        await verifyAndCommitAccountRemoval(result.removal);
        return {
          userID: authorId,
          reedID: targetReedId,
          kind: 'account',
          timestamp: result.removal.serverSignature?.timestamp,
        };
      }
      if (result.removal.type === 'reed') {
        await verifyAndCommitReedRemoval(result.removal);
        return {
          userID: result.removal.userID,
          reedID: result.removal.reedID,
          kind: 'reed',
          timestamp: result.removal.serverSignature?.timestamp,
        };
      }
    } catch (error) {
      console.warn('Quote: could not resolve gone reed', targetReedId, error);
    }
    return null;
  }

  async function loadUsername(userID) {
    try {
      const user = await userRepository.getByUserId(userID);
      return user?.username || userID;
    } catch {
      return userID;
    }
  }

  async function load(_id, ref, isMissing) {
    loading = true;
    loadFailed = false;
    displayReed = null;
    removedTarget = null;
    username = '';

    if (isMissing && !ref && !reed) {
      loading = false;
      return;
    }

    try {
      let source = reed ?? null;
      let parsed = null;

      if (!source && ref) {
        parsed = parseReedRef(ref);
        if (!parsed) {
          loadFailed = true;
          loading = false;
          return;
        }
        source = await reedsService.getReed(parsed.authorId, parsed.reedId);
        if (!source) {
          const gone = await resolveGone(parsed.authorId, parsed.reedId);
          if (gone) {
            removedTarget = gone;
            username = await loadUsername(gone.userID);
            loading = false;
            return;
          }
          loadFailed = true;
          loading = false;
          return;
        }
      }

      if (!source) {
        loadFailed = true;
        loading = false;
        return;
      }

      const resolved = await resolveBlankEchoChain(source, (authorId, targetReedId) =>
        reedsService.getReed(authorId, targetReedId)
      );
      displayReed = resolved;
      username = await loadUsername(resolved.userID);
    } catch (error) {
      console.error('Error loading quote:', error);
      if (reed) {
        displayReed = reed;
        username = await loadUsername(reed.userID);
      } else {
        loadFailed = true;
      }
    } finally {
      loading = false;
    }
  }

  function handleClick(event) {
    if (!linked) return;
    event.stopPropagation();
    if (displayReed) {
      goto(`/reed/${displayReed.userID}/${displayReed.id}`);
      return;
    }
    if (removedTarget) {
      goto(`/reed/${removedTarget.userID}/${removedTarget.reedID}`);
    }
  }
</script>

{#if missing || loadFailed}
  <div class="quote quote--missing" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Original reed unavailable</div>
  </div>
{:else if loading}
  <div class="quote" style="--border-color: {borderColor}">
    <div class="quote-meta">{icon} Loading...</div>
  </div>
{:else if removedTarget}
  {#if linked}
    <div
      class="quote quote--linked quote--removed"
      class:quote--clamped={maxLines > 0}
      style="--border-color: {borderColor}; --max-lines: {maxLines}"
      role="link"
      tabindex="0"
      on:click={handleClick}
      on:keydown={(e) => e.key === 'Enter' && handleClick(e)}
    >
      <div class="quote-meta">{icon} {label}{username} · {removedTarget.kind === 'account' ? 'Account deleted' : 'Removed'}</div>
      <p class="quote-removed-note">
        {removedTarget.kind === 'account'
          ? 'This author deleted their account.'
          : 'This reed was removed by the author.'}
      </p>
    </div>
  {:else}
    <div
      class="quote quote--removed"
      class:quote--clamped={maxLines > 0}
      style="--border-color: {borderColor}; --max-lines: {maxLines}"
    >
      <div class="quote-meta">{icon} {label}{username} · {removedTarget.kind === 'account' ? 'Account deleted' : 'Removed'}</div>
      <p class="quote-removed-note">
        {removedTarget.kind === 'account'
          ? 'This author deleted their account.'
          : 'This reed was removed by the author.'}
      </p>
    </div>
  {/if}
{:else if displayReed}
  {#if linked}
    <div
      class="quote quote--linked"
      class:quote--clamped={maxLines > 0}
      style="--border-color: {borderColor}; --max-lines: {maxLines}"
      role="link"
      tabindex="0"
      on:click={handleClick}
      on:keydown={(e) => e.key === 'Enter' && handleClick(e)}
    >
      <div class="quote-meta">{icon} {label}{username}{#if displayReed.serverSignature?.timestamp} · {formatRelativeTime(displayReed.serverSignature.timestamp)}{/if}</div>

      {#if (displayReed.content || '').trim()}
        <MarkdownParser text={displayReed.content} preview={true} className="quote-content" />
      {/if}
    </div>
  {:else}
    <div
      class="quote"
      class:quote--clamped={maxLines > 0}
      style="--border-color: {borderColor}; --max-lines: {maxLines}"
    >
      <div class="quote-meta">{icon} {label}{username}{#if displayReed.serverSignature?.timestamp} · {formatRelativeTime(displayReed.serverSignature.timestamp)}{/if}</div>

      {#if (displayReed.content || '').trim()}
        <MarkdownParser text={displayReed.content} preview={true} className="quote-content" />
      {/if}
    </div>
  {/if}
{/if}

<style>
  .quote {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-left: 3px solid var(--border-color);
    border-radius: 8px;
    background: var(--bg);
  }

  .quote--linked {
    cursor: pointer;
  }

  .quote--linked:hover {
    background: var(--input-bg);
  }

  .quote--clamped :global(.quote-content) {
    display: -webkit-box;
    -webkit-line-clamp: var(--max-lines);
    line-clamp: var(--max-lines);
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .quote-meta {
    font-size: 0.75rem;
    color: var(--muted);
    margin-bottom: 0.25rem;
    word-break: break-word;
  }

  :global(.quote-content) {
    font-size: 0.85rem;
    color: var(--fg);
    line-height: 1.4;
    white-space: pre-wrap;
  }

  .quote--missing .quote-meta {
    margin-bottom: 0;
    font-style: italic;
  }

  .quote-removed-note {
    margin: 0;
    font-size: 0.85rem;
    color: var(--muted);
    font-style: italic;
  }

  .quote--removed .quote-meta {
    margin-bottom: 0.15rem;
  }
</style>
