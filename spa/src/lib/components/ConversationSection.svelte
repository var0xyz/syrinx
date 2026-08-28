<script>
  import { goto } from '$app/navigation';
  import { apiService } from '$lib/services/api';
  import { reedsService } from '$lib/repositories/reeds';
  import { reedRepliesRepository } from '$lib/repositories/reedReplies';
  import { removedReedsRepository } from '$lib/repositories/removedReeds';
  import { userRepository } from '$lib/repositories/user';
  import { serverConnection } from '$lib/services/serverConnection';
  import { isOnline } from '$lib/services/pwa';
  import { formatRelativeTime } from '$lib/utils/time';
  import { parseReedRef } from '$lib/utils/identityRef';
  import { get } from 'svelte/store';
  import ReedAuthorHeader from '$lib/components/ReedAuthorHeader.svelte';
  import MarkdownParser from '$lib/components/MarkdownParser.svelte';

  /** Parent reed's own canonical ref (authorID@serverID/reedID). */
  export let parentReedRef = '';
  /** Thread wire ref for cache rows. */
  export let threadId = '';
  /** Bump to force reload (e.g. after FOLLOW_REED). */
  export let refreshToken = 0;
  /** Bound out to the parent for tab-count display and empty-state gating. */
  export let count = 0;

  /** @type {{ userID: string; reedID: string; reed?: import('$lib/types/reed').ReedType | null; username?: string; timestamp?: string; loading?: boolean; removed?: boolean }[]} */
  let rows = [];
  let hasMore = false;
  let loading = true;
  let loadingMore = false;
  let errorMessage = '';
  /** @type {Set<string>} */
  let pendingBodies = new Set();

  let lastLoadKey = '';

  $: loadKey = `${parentReedRef}:${refreshToken}`;
  $: if (loadKey && loadKey !== lastLoadKey) {
    lastLoadKey = loadKey;
    void loadConversation();
  }

  async function loadConversation() {
    loading = true;
    errorMessage = '';
    try {
      const cached = await reedRepliesRepository.listByParent(parentReedRef);
      rows = await hydrateRows(cached.map((r) => ({ userID: r.userID, reedID: r.reedID })));
      if ($isOnline) {
        await refreshFromServer(false);
      }
    } catch (err) {
      console.error('ConversationSection: load failed', err);
      errorMessage = 'Could not load conversation.';
    } finally {
      loading = false;
    }
  }

  async function refreshFromServer(showLoading = true) {
    if (!$isOnline || !parentReedRef) return;
    if (showLoading) loading = true;
    try {
      const res = await apiService.listReplies(parentReedRef);
      if (threadId) {
        await reedRepliesRepository.syncFromServerList(parentReedRef, threadId, res.replies);
        if (!res.hasMore) {
          await reedRepliesRepository.pruneStale(
            parentReedRef,
            new Set(res.replies.map((r) => r.reedID)),
          );
        }
      }
      hasMore = res.hasMore;
      rows = await hydrateRows(res.replies);
      errorMessage = '';
    } catch (err) {
      console.error('ConversationSection: server refresh failed', err);
      if (!rows.length) {
        errorMessage = 'Could not load conversation.';
      }
    } finally {
      if (showLoading) loading = false;
    }
  }

  function relayServerId() {
    const parsed = parseReedRef(threadId);
    if (parsed?.serverId) return parsed.serverId;
    return localStorage.getItem('serverId') || '';
  }

  async function applyReplyBody(reedID, reed) {
    if (!reed) return;
    const username =
      (await userRepository.getByUserId(reed.userID).catch(() => null))?.username ?? reed.userID;
    rows = rows.map((r) =>
      r.reedID === reedID
        ? {
            ...r,
            reed,
            username,
            timestamp: reed.serverSignature?.timestamp,
            loading: false,
          }
        : r,
    );
  }

  /** Issue REQUEST_REED relay for one reply and refresh the row when content arrives. */
  async function relayReply(ref) {
    if (!get(isOnline)) return;
    const serverId = relayServerId();
    if (!serverId) return;

    const held = await reedsService.getReed(ref.reedID);
    if (held) {
      await applyReplyBody(ref.reedID, held);
      return;
    }

    pendingBodies.add(ref.reedID);
    rows = rows.map((r) => (r.reedID === ref.reedID ? { ...r, loading: true } : r));

    try {
      await serverConnection.connect();
      const reed = await serverConnection.requestReedContent(ref.reedID, serverId);
      await applyReplyBody(ref.reedID, reed);
    } catch (err) {
      console.warn('ConversationSection: relay failed for reply', ref.reedID, err);
      rows = rows.map((r) => (r.reedID === ref.reedID ? { ...r, loading: false } : r));
    } finally {
      pendingBodies.delete(ref.reedID);
    }
  }

  function relayAllReplies(refs) {
    if (!get(isOnline)) return;
    for (const ref of refs) {
      void relayReply(ref);
    }
  }

  async function hydrateRows(refs) {
    /** @type {typeof rows} */
    const out = [];
    for (const ref of refs) {
      if (await removedReedsRepository.has(ref.reedID)) continue;
      const reed = await reedsService.getReed(ref.reedID);
      const username = reed
        ? (await userRepository.getByUserId(ref.userID).catch(() => null))?.username ?? ref.userID
        : ref.userID;
      out.push({
        userID: ref.userID,
        reedID: ref.reedID,
        reed,
        username,
        timestamp: reed?.serverSignature?.timestamp,
        loading: get(isOnline) && !reed,
      });
    }
    out.sort((a, b) => {
      const ta = a.timestamp ? Date.parse(a.timestamp) : 0;
      const tb = b.timestamp ? Date.parse(b.timestamp) : 0;
      if (ta !== tb) return ta - tb;
      return a.reedID.localeCompare(b.reedID);
    });
    relayAllReplies(out.map((r) => ({ userID: r.userID, reedID: r.reedID })));
    return out;
  }

  async function loadMore() {
    if (!hasMore || loadingMore || !rows.length) return;
    const oldest = rows[0];
    if (!oldest?.timestamp) return;
    loadingMore = true;
    try {
      const res = await apiService.listReplies(parentReedRef, { before: oldest.timestamp });
      if (threadId) {
        await reedRepliesRepository.syncFromServerList(parentReedRef, threadId, res.replies);
      }
      hasMore = res.hasMore;
      const older = await hydrateRows(res.replies);
      const seen = new Set(rows.map((r) => r.reedID));
      rows = [...older.filter((r) => !seen.has(r.reedID)), ...rows];
    } catch (err) {
      console.error('ConversationSection: load more failed', err);
    } finally {
      loadingMore = false;
    }
  }

  /** Called from parent when a new direct reply arrives via REED_REPLY.
   * Appends at the end without re-sorting or reloading — existing rows must
   * keep their identity so the list doesn't jump/reset the viewer's scroll
   * position. */
  export async function onReplyArrived(reed) {
    if (!reed?.replying || !reed.threadId) return;
    await reedRepliesRepository.upsertFromReed(reed);
    const reedID = reed.id;
    pendingBodies.delete(reedID);
    const username =
      (await userRepository.getByUserId(reed.userID).catch(() => null))?.username ?? reed.userID;
    const row = {
      userID: reed.userID,
      reedID,
      reed,
      username,
      timestamp: reed.serverSignature?.timestamp,
      loading: false,
    };
    if (rows.some((r) => r.reedID === reedID)) return;
    rows = [...rows, row];
  }

  /** Called from parent when a reply's removal cert commits locally (own
   * delete, or a live REED_REMOVED push). Splices the row out in place —
   * a full reload here would reset the viewer's scroll position. */
  export function onReplyRemoved(reedID) {
    rows = rows.filter((r) => r.reedID !== reedID);
  }

  function navigateToReply(row) {
    goto(`/reed/${row.reedID}`);
  }

  $: visible = !loading;
  $: count = rows.length;
</script>

{#if visible}
<section class="conversation-section" aria-label="Conversation">
  {#if rows.length === 0}
    <p class="conversation-empty">Start the conversation. Be the first to reply.</p>
  {/if}
  <ul class="reply-list">
    {#each rows as row (row.reedID)}
      <li>
        <div
          class="reply-row"
          class:reply-row--loading={row.loading}
          role="button"
          tabindex="0"
          on:click={() => navigateToReply(row)}
          on:keydown={(e) => e.key === 'Enter' && navigateToReply(row)}
        >
          <ReedAuthorHeader
            userID={row.userID}
            username={row.username}
            avatarSize="36px"
            subtext={row.timestamp ? formatRelativeTime(row.timestamp) : 'Waiting for reed...'}
            stopPropagation
            linked={false}
          />
          {#if !row.loading}
            <div class="reply-body">
              {#if row.reed}
                {#if row.reed.content?.trim()}
                  <div class="reply-preview">
                    <MarkdownParser text={row.reed.content} preview={true} />
                  </div>
                {:else}
                  <p class="reply-preview muted">Empty reply</p>
                {/if}
              {/if}
            </div>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
  {#if hasMore}
    <button type="button" class="load-more" disabled={loadingMore} on:click={loadMore}>
      {loadingMore ? 'Loading…' : 'Load more'}
    </button>
  {/if}
</section>
{/if}

<style>
  .conversation-section {
    padding-top: 1rem;
  }

  .conversation-empty {
    margin: 0 0.75rem 1rem;
    color: var(--muted);
    font-size: 0.9rem;
    font-style: italic;
    padding-left: 1rem;
  }

  .reply-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .reply-row {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    width: 100%;
    text-align: left;
    font: inherit;
    color: var(--fg);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.75rem;
    cursor: pointer;
  }

  .reply-row--loading {
    gap: 0;
  }

  .reply-row:hover {
    background: var(--border);
  }

  .reply-body {
    min-width: 0;
  }

  .reply-preview {
    font-size: 0.9rem;
    overflow: hidden;
    color: var(--fg);
  }

  .reply-preview.muted {
    color: var(--muted);
  }

  .load-more {
    margin-top: 0.75rem;
    width: 100%;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--fg);
    font: inherit;
    cursor: pointer;
  }

  .load-more:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
