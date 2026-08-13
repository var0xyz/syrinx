<script>
  import { onMount } from 'svelte';
  import { reedsService, unsignedReedsProcessed, profileReedQueue, followReedQueue } from '$lib/repositories/reeds';
  import { formatRelativeTime } from '$lib/utils/time';
  import { apiService } from '$lib/services/api';
  import { dbService } from '$lib/services/db';
  import { userRepository } from '$lib/repositories/user';
  import { removeReedAsAuthor } from '$lib/services/reedRemoval';
  import { pendingRemovalSynced } from '$lib/repositories/pendingRemoval';
  import NewReedModal from '$lib/components/NewReedModal.svelte';
  import Quote from '$lib/components/Quote.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import KebabMenu from '$lib/components/KebabMenu.svelte';
  import { goto } from '$app/navigation';
  import { parseReedRef } from '$lib/utils/reedRef';
  import { isBlankEcho, resolveBlankEchoFromMap } from '$lib/utils/emptyEcho';
  import { serverConnection } from '$lib/services/serverConnection';
  import { isOnline } from '$lib/services/pwa';
  import { restoreWindowScroll } from '$lib/utils/scrollSnapshot';
  import { verifyAndCommitReedRemoval } from '$lib/services/reedRemoval';
  import { verifyAndCommitAccountRemoval } from '$lib/services/accountRemoval';
  import { removedReedsRepository } from '$lib/repositories/removedReeds';
  import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
  import { get } from 'svelte/store';

  export let authorId;
  export let isOwner = false;
  export let showWriteButton = false;
  /** Server already reports this author has reeds (info.hasReeds), but none
   * are held locally yet — e.g. the author is offline and no online peer
   * has relayed the body. Distinguishes "known content, still arriving"
   * from a genuinely empty author. */
  export let expectContent = false;
  /** Window scrollY to restore after the first load (SvelteKit page snapshot). */
  export let scrollRestoreY = /** @type {number | null} */ (null);

  let isWriteSectionOpen = false;
  let showNewReedBanner = false;
  let reeds = [];
  /** @type {import('$lib/types/reed').ReedType[]} */
  let pendingReeds = [];
  let profileUser = null;
  let loadingReeds = true;
  let errorLoadingReeds = '';
  let echoedReeds = new Map();
  let repliedToReeds = new Map();
  let echoedReedUsers = new Map();
  /** Echo refs we already asked the server for (avoid duplicate REQUEST_REED). */
  let pendingEchoRequests = new Set();
  /** Echo refs confirmed gone — stop showing "Loading echoed reed…" forever
   * for these. Value is the known reason: 'account' (author's account was
   * removed, cert in hand), 'reed' (the reed itself was removed, cert in
   * hand), or 'unknown' (server says not_found, no cert either way — the
   * only case that stays hedged instead of stating a fact we don't have). */
  let unavailableEchoKeys = new Map();

  /**
   * We already have the answer locally most of the time: removedReeds and
   * removedAccounts are populated by every removal cert this client has
   * ever seen (WS push, profile/reed page fetches, etc.). Checked before
   * ever dispatching a relay request — the request drainer only sends one
   * REQUEST_REED per second, so anything routed through it first pays a
   * multi-second delay for content we could've ruled out instantly.
   * Returns the known reason, or null if not locally known.
   */
  async function locallyKnownRemovalReason(authorId, reedId) {
    if (await removedAccountsRepository.get(authorId)) return 'account';
    const reedCert = await removedReedsRepository.get(reedId);
    if (reedCert && reedCert.userID === authorId) return 'reed';
    return null;
  }

  /**
   * A passive relay fetch (requestReedContent) can hang forever if no peer
   * ever holds the target — including "removed", the common real case.
   * Only called after a relay miss; locallyKnownRemovalReason already ruled
   * out the fast local answer before we got this far.
   */
  async function checkEchoUnavailable(key, authorId, reedId) {
    try {
      const localReason = await locallyKnownRemovalReason(authorId, reedId);
      if (localReason) {
        unavailableEchoKeys = new Map(unavailableEchoKeys).set(key, localReason);
        return;
      }

      if (!get(isOnline)) return;
      const result = await apiService.getReedOrRemoval(authorId, reedId);
      if (result.kind === 'reed') return;
      let reason = 'unknown';
      if (result.kind === 'gone' && result.removal && 'type' in result.removal) {
        if (result.removal.type === 'account') {
          await verifyAndCommitAccountRemoval(result.removal);
          reason = 'account';
        } else if (result.removal.type === 'reed') {
          await verifyAndCommitReedRemoval(result.removal);
          reason = 'reed';
        }
      }
      unavailableEchoKeys = new Map(unavailableEchoKeys).set(key, reason);
    } catch (error) {
      console.warn('ReedsList: could not resolve echo target', key, error);
    }
  }
  /** profileReedQueue / followReedQueue items already handled (store value is sticky). */
  let lastHandledProfileReedId = '';
  let lastHandledFollowReedId = '';
  let appliedScrollRestore = false;
  /** authorId a profileUser fetch is in flight for/last completed for — lets
   * a slow response for a since-superseded authorId (e.g. profile A -> B
   * client-side nav) be dropped instead of clobbering profile B's data. */
  let profileUserFor = '';

  $: if (authorId && authorId !== profileUserFor) {
    profileUserFor = authorId;
    void loadProfileUser(authorId);
  }

  async function loadProfileUser(id) {
    const user = await userRepository.getByUserId(id).catch(() => null);
    if (id !== profileUserFor) return;
    profileUser = user;
  }

  $: if ($unsignedReedsProcessed > 0) loadReeds();
  $: if ($pendingRemovalSynced > 0) loadReeds();

  // Only depend on the queue store — never read reeds/pendingReeds here or
  // loadReeds() will retrigger this block and flash the loading screen.
  $: profileArrived = $profileReedQueue?.reed;
  $: if (profileArrived && profileArrived.id !== lastHandledProfileReedId) {
    lastHandledProfileReedId = profileArrived.id;
    void onProfileReedArrived(profileArrived);
  }

  $: followArrived = $followReedQueue?.reed;
  $: if (followArrived?.userID === authorId && followArrived.id !== lastHandledFollowReedId) {
    lastHandledFollowReedId = followArrived.id;
    showNewReedBanner = true;
  }

  onMount(async () => {
    await loadReeds();
  });

  async function mergeEchoOriginal(original, echoRefKey) {
    pendingEchoRequests.delete(echoRefKey);
    const echoMap = new Map(echoedReeds);
    echoMap.set(echoRefKey, original);

    // Continue blank-echo chain if the arrived original is itself blank.
    let walk = original;
    for (let i = 0; i < 8 && isBlankEcho(walk) && walk.echoing; i++) {
      if (echoMap.has(walk.echoing)) {
        walk = echoMap.get(walk.echoing);
        continue;
      }
      const parsed = parseReedRef(walk.echoing);
      if (!parsed) break;
      const nested = await reedsService.getReed(parsed.authorId, parsed.reedId);
      if (nested) {
        echoMap.set(walk.echoing, nested);
        walk = nested;
        continue;
      }
      if (!pendingEchoRequests.has(walk.echoing)) {
        pendingEchoRequests.add(walk.echoing);
        const echoingRef = walk.echoing;
        const localReason = await locallyKnownRemovalReason(parsed.authorId, parsed.reedId);
        if (localReason) {
          unavailableEchoKeys = new Map(unavailableEchoKeys).set(echoingRef, localReason);
        } else {
          serverConnection
            .requestReedContent(parsed.reedId, parsed.authorId, parsed.serverId)
            .catch(() => checkEchoUnavailable(echoingRef, parsed.authorId, parsed.reedId));
        }
      }
      break;
    }
    echoedReeds = echoMap;

    try {
      const unwrapped = resolveBlankEchoFromMap(
        /** @type {any} */ ({ id: '_', content: '', echoing: echoRefKey }),
        echoMap
      );
      const author = await userRepository.getByUserId(unwrapped.userID);
      if (author) {
        const userMap = new Map(echoedReedUsers);
        userMap.set(unwrapped.userID, author);
        echoedReedUsers = userMap;
      }
    } catch {
      // username falls back to userID in the template
    }
  }

  async function onProfileReedArrived(arrived) {
    const all = [...pendingReeds, ...reeds];
    const echoRef = all.find((r) => {
      if (!isBlankEcho(r)) return false;
      const parsed = parseReedRef(r.echoing);
      return parsed && parsed.reedId === arrived.id && parsed.authorId === arrived.userID;
    })?.echoing;

    if (echoRef) {
      await mergeEchoOriginal(arrived, echoRef);
      return;
    }

    if (arrived.userID === authorId) {
      if (window.scrollY === 0) {
        await loadReeds();
      } else {
        showNewReedBanner = true;
      }
    }
  }

  async function loadReeds() {
    try {
      loadingReeds = true;
      errorLoadingReeds = '';
      reeds = await reedsService.getReedsByAuthor(authorId);
      pendingReeds = isOwner
        ? await reedsService.getUnsignedReedsByAuthor(authorId)
        : [];

      const allForQuotes = [...pendingReeds, ...reeds];

      // Fetch echoed reeds; walk blank-echo chains so unwrap can reach content.
      const echoMap = new Map();
      const seenEchoKeys = new Set();
      /** @type {{ key: string, author: string, reedId: string }[]} */
      let echoFrontier = [];

      function enqueueEchoKey(key) {
        if (!key || seenEchoKeys.has(key)) return;
        const parsed = parseReedRef(key);
        if (!parsed) return;
        seenEchoKeys.add(key);
        echoFrontier.push({ key, author: parsed.authorId, reedId: parsed.reedId });
      }

      for (const r of allForQuotes) {
        if (r.echoing) enqueueEchoKey(r.echoing);
      }

      for (let depth = 0; depth < 8 && echoFrontier.length > 0; depth++) {
        const batch = echoFrontier;
        echoFrontier = [];
        const echoResults = await Promise.allSettled(
          batch.map(({ author, reedId }) => reedsService.getReed(author, reedId))
        );
        batch.forEach(({ key, author, reedId }, i) => {
          if (echoResults[i].status === 'fulfilled' && echoResults[i].value) {
            const original = echoResults[i].value;
            echoMap.set(key, original);
            pendingEchoRequests.delete(key);
            if (isBlankEcho(original) && original.echoing) {
              enqueueEchoKey(original.echoing);
            }
            return;
          }
          const parsed = parseReedRef(key);
          if (!parsed) return;
          if (
            allForQuotes.some((r) => r.echoing === key && isBlankEcho(r)) &&
            !pendingEchoRequests.has(key)
          ) {
            pendingEchoRequests.add(key);
            void (async () => {
              const localReason = await locallyKnownRemovalReason(author, reedId);
              if (localReason) {
                unavailableEchoKeys = new Map(unavailableEchoKeys).set(key, localReason);
                return;
              }
              serverConnection
                .requestReedContent(reedId, author, parsed.serverId)
                .catch(() => checkEchoUnavailable(key, author, reedId));
            })();
          }
        });
      }
      echoedReeds = echoMap;

      // Authors for blank-echo identity swap (final unwrapped reed).
      const displayAuthors = new Set();
      for (const r of allForQuotes) {
        if (!isBlankEcho(r)) continue;
        const display = resolveBlankEchoFromMap(r, echoMap);
        if (display.id !== r.id) displayAuthors.add(display.userID);
      }
      const echoAuthorResults = await Promise.allSettled(
        [...displayAuthors].map((author) => userRepository.getByUserId(author))
      );
      const userMap = new Map();
      [...displayAuthors].forEach((author, i) => {
        if (echoAuthorResults[i].status === 'fulfilled' && echoAuthorResults[i].value) {
          userMap.set(author, echoAuthorResults[i].value);
        }
      });
      echoedReedUsers = userMap;

      // Prefetch replied-to reeds for list items and blank-unwrapped targets.
      const replyKeys = new Set();
      for (const r of allForQuotes) {
        const display = resolveBlankEchoFromMap(r, echoMap);
        if (display.replying) replyKeys.add(display.replying);
      }
      const replyEntries = [...replyKeys]
        .map((key) => {
          const parsed = parseReedRef(key);
          if (!parsed) return null;
          return { key, author: parsed.authorId, reedId: parsed.reedId };
        })
        .filter(Boolean);

      const replyResults = await Promise.allSettled(
        replyEntries.map(({ author, reedId }) => reedsService.getReed(author, reedId))
      );

      const replyMap = new Map();
      replyEntries.forEach(({ key }, i) => {
        if (replyResults[i].status === 'fulfilled' && replyResults[i].value) {
          replyMap.set(key, replyResults[i].value);
        }
      });
      repliedToReeds = replyMap;
    } catch (error) {
      console.error('Error loading reeds:', error);
      errorLoadingReeds = 'Failed to load reeds';
    } finally {
      loadingReeds = false;
      if (!appliedScrollRestore && typeof scrollRestoreY === 'number') {
        appliedScrollRestore = true;
        await restoreWindowScroll(scrollRestoreY);
      }
    }
  }

  async function deleteReed(reedId, pending = false) {
    if (!confirm('Are you sure you want to delete this reed?')) {
      return;
    }

    try {
      if (pending) {
        await reedsService.discardUnsignedReed(reedId);
        pendingReeds = pendingReeds.filter((reed) => reed.id !== reedId);
      } else {
        await removeReedAsAuthor(authorId, reedId);
        reeds = reeds.filter(reed => reed.id !== reedId);
      }
    } catch (error) {
      console.error('Error deleting reed:', error);
    }
  }

  function echoUnavailableMessage(reason) {
    if (reason === 'account') return 'Echoed reed unavailable — the author deleted their account';
    if (reason === 'reed') return 'Echoed reed unavailable — the author removed it';
    return 'Echoed reed unavailable';
  }

  function navigateToReed(reed) {
    goto(`/reed/${reed.userID}/${reed.id}`);
  }
</script>

{#if showWriteButton}
  <button class="floating-write-btn" on:click={() => (isWriteSectionOpen = true)}>
    <span class="icon">✍️</span>
  </button>
  <NewReedModal open={isWriteSectionOpen} on:close={() => (isWriteSectionOpen = false)} />
{/if}

{#if showNewReedBanner}
  <div class="new-reed-banner">
    <div class="new-reed-msg">New reed available</div>
    <button on:click={() => { showNewReedBanner = false; void loadReeds(); }}>Show</button>
    <button class="dismiss" on:click={() => (showNewReedBanner = false)}>✕</button>
  </div>
{/if}

<div class="reeds-list">
  {#if loadingReeds}
    <div class="loading">
      <h2>Loading reeds...</h2>
      <p>Please wait while we fetch your reeds.</p>
    </div>
  {:else if errorLoadingReeds}
    <div class="error-state">
      <div class="error-icon">⚠️</div>
      <h3>Error loading reeds</h3>
      <p>{errorLoadingReeds}</p>
      <button class="btn btn-primary" on:click={loadReeds}>Try Again</button>
    </div>
  {:else if reeds.length === 0 && pendingReeds.length === 0 && expectContent}
    <div class="empty-state">
      <div class="empty-icon">🌱</div>
      <h3>Waiting for content…</h3>
      <p>Reeds will appear automatically appear here once we find a peer to fetch them from.</p>
    </div>
  {:else if reeds.length === 0 && pendingReeds.length === 0}
    <div class="empty-state">
      <div class="empty-icon">{isOwner ? '🌾' : '🫙'}</div>
      <h3>No reeds yet</h3>
      <p>{isOwner ? 'Your reeds will appear here when you publish them.' : 'New reeds will appear here once we receive them.'}</p>
    </div>
  {:else}
    {#each pendingReeds as reed (reed.id)}
      {@const displayReed = resolveBlankEchoFromMap(reed, echoedReeds)}
      {@const isUnwrapped = isBlankEcho(reed) && displayReed.id !== reed.id}
      {@const awaitingOriginal =
        isBlankEcho(reed) &&
        isBlankEcho(displayReed) &&
        !(displayReed.echoing && echoedReeds.has(displayReed.echoing))}
      {@const echoUnavailableReason = awaitingOriginal ? unavailableEchoKeys.get(displayReed.echoing) : undefined}
      {@const echoUnavailable = !!echoUnavailableReason}
      {@const displayUser = isUnwrapped ? (echoedReedUsers.get(displayReed.userID) || { username: displayReed.userID }) : (profileUser || { username: authorId })}
      <div class="reed-item pending" role="button" tabindex="0" on:click={() => navigateToReed(reed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(reed)}>
        <div class="reed-header">
          <ReedAuthorHeader
            userID={displayReed.userID}
            serverID={displayReed.serverSignature?.serverID ?? ''}
            username={displayUser.username}
            nameTag="h3"
            subtext="Pending…"
            subtextClass="pending"
            stopPropagation
            linked={false}
          />
          {#if isOwner}
            <div class="reed-meta">
              <KebabMenu options={[{ label: 'Delete', danger: true, icon: '/icons/trash-16.png', onSelect: () => deleteReed(reed.id, true) }]} />
            </div>
          {/if}
        </div>
        {#if !awaitingOriginal && displayReed.replying}
          <div class="quote-container">
            <Quote
              reed={repliedToReeds.get(displayReed.replying)}
              reedRef={displayReed.replying}
              type="reply"
              missing={false}
              linked={false}
            />
          </div>
        {/if}
        {#if echoUnavailable}
          <div class="reed-preview echo placeholder">
            <span class="placeholder-label">{echoUnavailableMessage(echoUnavailableReason)}</span>
          </div>
        {:else if awaitingOriginal}
          <div class="reed-preview echo placeholder" aria-busy="true">
            <span class="placeholder-label">Loading echoed reed…</span>
          </div>
        {:else if (displayReed.content || "").trim()}
          <div class={["reed-preview", !isUnwrapped && reed.echoing && "echo", !isUnwrapped && reed.replying && "reply"]}>
            <MarkdownParser text={displayReed.content} preview={true} />
          </div>
        {/if}
        {#if !awaitingOriginal && displayReed.echoing}
          <div class="quote-container">
            <Quote
              reed={echoedReeds.get(displayReed.echoing)}
              reedRef={displayReed.echoing}
              type="echo"
              missing={false}
              linked={false}
            />
          </div>
        {/if}
      </div>
    {/each}
    {#each reeds as reed (reed.id)}
      {@const displayReed = resolveBlankEchoFromMap(reed, echoedReeds)}
      {@const isUnwrapped = isBlankEcho(reed) && displayReed.id !== reed.id}
      {@const awaitingOriginal =
        isBlankEcho(reed) &&
        isBlankEcho(displayReed) &&
        !(displayReed.echoing && echoedReeds.has(displayReed.echoing))}
      {@const echoUnavailableReason = awaitingOriginal ? unavailableEchoKeys.get(displayReed.echoing) : undefined}
      {@const echoUnavailable = !!echoUnavailableReason}
      {@const displayUser = isUnwrapped ? (echoedReedUsers.get(displayReed.userID) || { username: displayReed.userID }) : (profileUser || { username: authorId })}
      <div class="reed-item" role="button" tabindex="0" on:click={() => navigateToReed(awaitingOriginal ? reed : displayReed)} on:keydown={(e) => e.key === 'Enter' && navigateToReed(awaitingOriginal ? reed : displayReed)}>
        <div class="reed-header">
          <ReedAuthorHeader
            userID={displayReed.userID}
            serverID={displayReed.serverSignature?.serverID ?? ''}
            username={displayUser.username}
            nameTag="h3"
            subtext={formatRelativeTime((awaitingOriginal ? reed : displayReed).serverSignature?.timestamp ?? reed.serverSignature?.timestamp)}
            stopPropagation
            linked={false}
          />
          {#if isOwner}
            <div class="reed-meta">
              <KebabMenu options={[{ label: 'Delete', danger: true, icon: '/icons/trash-16.png', onSelect: () => deleteReed(reed.id) }]} />
            </div>
          {/if}
        </div>
        {#if !awaitingOriginal && displayReed.replying}
          <div class="quote-container">
            <Quote
              reed={repliedToReeds.get(displayReed.replying)}
              reedRef={displayReed.replying}
              type="reply"
              missing={false}
              linked={false}
            />
          </div>
        {/if}
        {#if echoUnavailable}
          <div class="reed-preview echo placeholder">
            <span class="placeholder-label">{echoUnavailableMessage(echoUnavailableReason)}</span>
          </div>
        {:else if awaitingOriginal}
          <div class="reed-preview echo placeholder" aria-busy="true">
            <span class="placeholder-label">Loading echoed reed…</span>
          </div>
        {:else if (displayReed.content || "").trim()}
          <div class={["reed-preview", !isUnwrapped && reed.echoing && "echo", !isUnwrapped && reed.replying && "reply"]}>
            <MarkdownParser text={displayReed.content} preview={true} />
          </div>
        {/if}
        {#if !awaitingOriginal && displayReed.echoing}
          <div class="quote-container">
            <Quote
              reed={echoedReeds.get(displayReed.echoing)}
              reedRef={displayReed.echoing}
              type="echo"
              missing={false}
              linked={false}
            />
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>

<style>
  .new-reed-banner {
    position: fixed;
    top: 1rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    background: var(--surface);
    border: 1px solid var(--primary);
    border-radius: 8px;
    font-size: 0.9rem;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    width: calc(100vw - 2.5rem);
  }

  .new-reed-banner .new-reed-msg {
    flex-grow: 1;
    color: var(--fg);
  }

  .new-reed-banner button {
    flex-shrink: 0;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    border-radius: 4px;
    padding: 0.25rem 0.75rem;
    cursor: pointer;
    font-size: 0.85rem;
    white-space: nowrap;
    width: 5rem;
  }

  .new-reed-banner button.dismiss {
    flex-shrink: 0;
    background: none;
    color: var(--muted);
    padding: 0.25rem;
    cursor: pointer;
    width: 2rem;
  }

  .floating-write-btn {
    position: fixed;
    bottom: 80px;
    right: 20px;
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--button-text);
    border: none;
    cursor: pointer;
    box-shadow: 0 4px 12px rgba(88, 166, 255, 0.3);
    transition: all 0.2s ease;
    z-index: 1000;
  }

  .floating-write-btn:hover {
    transform: translateY(-2px);
    box-shadow: 0 6px 16px rgba(88, 166, 255, 0.4);
  }

  .floating-write-btn .icon {
    font-size: 1.5rem;
  }

  .reeds-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .reed-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    transition: all 0.2s ease;
    cursor: pointer;
  }

  .reed-item:hover {
    border-color: var(--primary);
    box-shadow: 0 2px 8px rgba(88, 166, 255, 0.1);
  }

  .reed-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--border);
    min-width: 0;
  }

  .error-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .error-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .error-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .error-state p {
    margin: 0 0 1rem 0;
    font-size: 0.9rem;
  }

  .reed-meta {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.25rem;
  }

  .reed-preview {
    padding: 1rem;
    word-break: break-word;
  }

  .reed-preview.reply {
    padding-top: 0;
    padding-left: 2rem;
  }

  .reed-preview.echo {
    padding-bottom: 0;
  }

  .reed-preview.placeholder {
    min-height: 4.5rem;
    display: flex;
    align-items: center;
    padding-bottom: 1rem;
  }

  .placeholder-label {
    color: var(--muted);
    font-size: 0.9rem;
    font-style: italic;
  }

  .quote-container {
    margin: 1rem;
  }

  .empty-state {
    text-align: center;
    padding: 3rem 1rem;
    color: var(--muted);
  }

  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
  }

  .empty-state h3 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
    font-size: 1.1rem;
  }

  .empty-state p {
    margin: 0;
    font-size: 0.9rem;
  }

  .loading {
    text-align: center;
    padding: 2rem;
    color: var(--muted);
  }

  .loading h2 {
    margin: 0 0 0.5rem 0;
    color: var(--fg);
  }

  .loading p {
    margin: 0;
  }

  @media (max-width: 768px) {
    .reeds-list {
      gap: 0.5rem;
    }

    .reed-header {
      padding: 0.75rem;
    }

    .reed-preview {
      padding: 0.5rem 0.75rem;
    }

    .reed-preview.reply {
      padding-top: 0;
      padding-left: 1.5rem;
    }

    .quote-container {
      margin: 0.75rem;
    }
  }
</style>
