/**
 * Reeds Service
 * Handles reed creation, storage, and retrieval
 */

import { apiService as api, canonicalKeyId } from '../services/api';
import { dbService } from '../services/db';
import { publicKeyRepository } from './publicKey';
import { userRepository } from './user';
import { Reed as ReedClass, type ReedType } from '$lib/types/reed';
import type { User } from '$lib/types/api';
import { serverConnection } from '$lib/services/serverConnection';
import { pendingPublicationRepository } from './pendingPublication';
import { get, writable } from 'svelte/store';
import { allowUnsigned, verifyReed } from '$lib/verifiers';
import {
  MAX_REED_RAW_CHARS,
  MAX_REED_VISIBLE_CHARS,
  reedContentWithinLimits,
} from '$lib/utils/reedContent';
import { isOnline, onReconnect } from '$lib/services/pwa';
import { isBlankEcho } from '$lib/utils/emptyEcho';
import { clearPublishTipOverride, previousIDForPublish } from '../services/publishTip';
import { userListsRepository } from './userLists';
import { tagsRepository } from './tags';
import { isOverThreshold } from '$lib/services/quota';
import { freeSpace } from '$lib/services/eviction';
import { blockedByRepository } from '$lib/repositories/blockedBy';
import type { UserListType } from '$lib/types/userList';

// Incremented each time processUnsignedReeds completes successfully
export const unsignedReedsProcessed = writable(0);

export type QueuedReed = {
  reed: ReedType;
  /** Server-sourced display label for ephemeral broadcast deliveries. */
  username?: string;
};


// Receives profile_subscription and request_reed deliveries (explicitly requested content)
export const profileReedQueue = writable<QueuedReed | null>(null);

// Receives FOLLOW_REED deliveries (follow-feed fanout + catch-up)
export const followReedQueue = writable<QueuedReed | null>(null);

// Receives broadcast_reed deliveries — ephemeral, NOT stored in IndexedDB
export const broadcastReedQueue = writable<QueuedReed | null>(null);

// Receives PIPE_REED deliveries (pipe subscription push; stored like follow reeds)
export const pipeReedQueue = writable<QueuedReed | null>(null);

// Receives REED_REPLY deliveries (reed-subscription push for a new reply
// somewhere in the subscribed thread) — kept separate from followReedQueue:
// the recipient isn't necessarily following the reply's author, so folding
// it into the follow feed / "new reed" banners would misattribute it.
export const reedReplyQueue = writable<QueuedReed | null>(null);

// Receives MENTION deliveries (live push + catch-up on reconnect)
export const mentionReedQueue = writable<QueuedReed | null>(null);

export function dispatchReedToQueue(
  reed: ReedType,
  eventName: string,
  username?: string
): void {
  const queued: QueuedReed = { reed, username };
  if (eventName === 'follow_reed') {
    followReedQueue.set(queued);
  } else if (eventName === 'broadcast_reed') {
    broadcastReedQueue.set(queued);
  } else if (eventName === 'pipe_reed') {
    pipeReedQueue.set(queued);
  } else if (eventName === 'reed_reply') {
    reedReplyQueue.set(queued);
  } else if (eventName === 'mention') {
    mentionReedQueue.set(queued);
  } else {
    profileReedQueue.set(queued);
  }
}


class ReedsService {
  /**
   * Persist the signed reed locally (unsignedReeds) and return immediately.
   * `publish` resolves true when the server countersigns, false if still pending.
   * Throws if local storage / validation fails.
   */
  async createReed(reed: ReedClass): Promise<{ publish: Promise<boolean> }> {
    if (!reedContentWithinLimits(reed.content)) {
      throw new Error(
        reed.content.length > MAX_REED_RAW_CHARS
          ? `Reed exceeds ${MAX_REED_RAW_CHARS} raw characters`
          : `Reed exceeds ${MAX_REED_VISIBLE_CHARS} visible characters`
      );
    }

    if (!reed.userSignature?.armor) {
      throw new Error('Reed is missing userSignature');
    }

    await dbService.put('unsignedReeds', reed.asObject(), allowUnsigned);
    unsignedReedsProcessed.update((n) => n + 1);

    return { publish: this.publishUnsignedReed(reed) };
  }

  /** In-flight countersignatures keyed by reed id (dedupes concurrent publish). */
  private publishing = new Map<string, Promise<boolean>>();

  /**
   * Countersign a pending reed; on success move it into the published store.
   * Concurrent calls for the same id share one request.
   */
  async publishUnsignedReed(reed: ReedClass | ReedType): Promise<boolean> {
    const existing = this.publishing.get(reed.id);
    if (existing) return existing;

    const run = this.countersignReed(reed).finally(() => {
      this.publishing.delete(reed.id);
    });
    this.publishing.set(reed.id, run);
    return run;
  }

  private async countersignReed(reed: ReedClass | ReedType): Promise<boolean> {
    const armor = reed.userSignature?.armor;
    if (!armor) {
      console.error('Skipping reed without userSignature:', reed.id);
      return false;
    }
    if (!get(isOnline)) {
      return false;
    }
    try {
      console.log('Getting signature from server...');
      const previousID = await previousIDForPublish();
      const response = await api.createReed(reed.id, armor, {
        echoing: reed.echoing,
        replyingTo: reed.replying?.to,
        tags: reed.tags,
        mentions: reed.mentions,
        ...(previousID ? { previousId: previousID } : {}),
      });
      let published: ReedType;
      if (reed instanceof ReedClass) {
        reed.applyServerResponse(response);
        published = reed.asObject();
      } else {
        published = { ...reed, serverSignature: response };
      }
      await this.storeReed(published);
      clearPublishTipOverride();
      await pendingPublicationRepository.put(published.id);
      await serverConnection.connect();
      const broadcast = !isBlankEcho(published);
      await serverConnection.publishReady(published.id, { broadcast });
      await dbService.delete('unsignedReeds', published.id);
      unsignedReedsProcessed.update((n) => n + 1);
      return true;
    } catch (error: any) {
      const isReedFork =
        error?.status === 409 && !!error?.message?.includes('current tip');
      if (isReedFork) {
        // Another session (dual-device/dual-tab) published past this
        // client's believed tip. previousIDForPublish() always recomputes
        // live from local reed state (no caching) — it will pick up the
        // winning publish once it syncs locally, so the existing
        // requeue-and-retry-on-reconnect path self-heals. Logged distinctly
        // so a stuck retry loop is diagnosable instead of reading as a
        // generic network failure.
        console.warn('Reed publish rejected as a history fork; will retry once local tip catches up:', reed.id);
      } else {
        console.error('Failed to publish reed to server, queued for later:', error);
      }
      return false;
    }
  }

  /**
   * Process any unsigned reeds that were stored locally but not yet confirmed by the server.
   * Should be called on app startup and when the app comes back online.
   */
  async processUnsignedReeds(): Promise<void> {
    const unsignedReeds = await dbService.getAll<ReedType>('unsignedReeds');
    if (unsignedReeds.length === 0) return;

    for (const reed of unsignedReeds) {
      await this.publishUnsignedReed(reed);
    }

    unsignedReedsProcessed.update(n => n + 1);
  }

  /**
   * Get a specific reed by its canonical id (published / countersigned only).
   */
  async getReed(reedId: string): Promise<ReedType | null> {
    try {
      return await dbService.get<ReedType>('reeds', reedId);
    } catch (error) {
      console.error('Failed to get reed:', error);
      throw error;
    }
  }

  /** Local pending reed (signed by user, not yet countersigned). */
  async getUnsignedReed(reedId: string): Promise<ReedType | null> {
    try {
      return (await dbService.get<ReedType>('unsignedReeds', reedId)) ?? null;
    } catch (error) {
      console.error('Failed to get unsigned reed:', error);
      throw error;
    }
  }

  /** Drop a pending reed that never reached the server. */
  async discardUnsignedReed(reedId: string): Promise<void> {
    await dbService.delete('unsignedReeds', reedId);
    unsignedReedsProcessed.update((n) => n + 1);
  }

  /** @deprecated Prefer storeReed — verification runs inside put. */
  async validateReed(reed: ReedType): Promise<boolean> {
    return verifyReed(reed);
  }

  /**
   * Persist a countersigned reed. Verification (author + server) runs in
   * `dbService.put` via `verifyReed`.
   *
   * Over the quota threshold, a victim is queued for eviction and drained
   * in the background — storing never waits on the server, since 85% of
   * quota still leaves room for this reed.
   */
  async storeReed(reed: ReedType): Promise<void> {
    // Nothing of a user who blocked the viewer is kept.
    if (reed.userID && (await blockedByRepository.has(reed.userID))) return;

    if (await isOverThreshold()) {
      await freeSpace();
    }

    // Ensure author key is cached (verifyReed needs armor; put attests).
    if (reed.userSignature?.id && reed.userID) {
      const fp = reed.userSignature.id;
      if (!(await publicKeyRepository.hasPublicKey(fp))) {
        try {
          const key = await api.getPublicKey(canonicalKeyId(reed.userID, fp));
          await publicKeyRepository.put(key);
        } catch (error) {
          console.error('Failed to cache author public key before reed store:', error);
        }
      }
    }

    // A removed thread's parts are dropped, never kept: this also finishes
    // a removal a crash cut short.
    if (reed.thread?.head) {
      const { threadsRepository } = await import('$lib/repositories/threads');
      if (await threadsRepository.isRemoved(reed.thread.head)) {
        await dbService.delete('reeds', reed.id);
        await tagsRepository.removeReed(reed.id);
        return;
      }
    }
    await dbService.put('reeds', reed, verifyReed);

    if (reed.userID) {
      try {
        await userRepository.getByUserId(reed.userID);
      } catch (error) {
        console.error('Failed to cache author profile after reed store:', error);
      }
    }

    if (reed.replying) {
      const { reedRepliesRepository } = await import('$lib/repositories/reedReplies');
      await reedRepliesRepository.upsertFromReed(reed);
    }

    await tagsRepository.add(reed);
  }

  async deleteReedsByAuthor(authorId: string): Promise<void> {
    const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', authorId);
    await Promise.all(reeds.map(r => dbService.delete('reeds', r.id)));
    await Promise.all(reeds.map(r => tagsRepository.removeReed(r.id)));
  }

  /**
   * Published reeds for an author, newest first. IndexedDB returns ascending id;
   * UUID v7 ids are time-ordered, so reverse is enough.
   */
  async getReedsByAuthor(authorId: string): Promise<ReedType[]> {
    try {
      const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', authorId);
      return reeds.reverse();
    } catch (error) {
      console.error('Failed to get reeds by author:', error);
      return [];
    }
  }

  /** One page of an author's locally-held reeds, newest first. The index
   * spans all authors, so the predicate filters as the cursor walks —
   * the same trade localSearch's searchReeds makes. */
  async getReedsByAuthorPage(
    authorId: string,
    limit: number,
    after?: number
  ): Promise<{ items: ReedType[]; hasMore: boolean; nextCursor?: number }> {
    try {
      const matched = await dbService.getLatestFromIndex<ReedType>(
        'reeds',
        'serverSignature.signedAt',
        limit + 1,
        (reed) => reed.userID === authorId,
        after
      );
      const hasMore = matched.length > limit;
      const items = matched.slice(0, limit);
      const last = items[items.length - 1];
      return { items, hasMore, nextCursor: last?.serverSignature?.signedAt ?? after };
    } catch (error) {
      console.error('Failed to get reeds page by author:', error);
      return { items: [], hasMore: false, nextCursor: after };
    }
  }

  /** Whether this device holds any reed by this author. */
  async hasReedsByAuthor(authorId: string): Promise<boolean> {
    try {
      const found = await dbService.getLatestFromIndex<ReedType>(
        'reeds',
        'serverSignature.signedAt',
        1,
        (reed) => reed.userID === authorId
      );
      return found.length > 0;
    } catch (error) {
      console.error('Failed to check reeds by author:', error);
      return false;
    }
  }

  /** Pending reeds for this author (local unsigned store only). */
  async getUnsignedReedsByAuthor(authorId: string): Promise<ReedType[]> {
    try {
      const all = await dbService.getAll<ReedType>('unsignedReeds');
      return all.filter((r) => r.userID === authorId);
    } catch (error) {
      console.error('Failed to get unsigned reeds:', error);
      return [];
    }
  }

  /** One page of local reeds under a tag, newest publication first. Pass the
   * previous page's nextCursor as `after` to resume. */
  async getReedsByTag(
    tag: string,
    limit: number,
    after?: string
  ): Promise<{ items: ReedType[]; authors: Record<string, User>; hasMore: boolean; nextCursor?: string }> {
    const normalized = tag.trim().replace(/^#/, '').toLowerCase();
    if (!normalized) return { items: [], authors: {}, hasMore: false };

    const rows = await tagsRepository.page(normalized, limit + 1, after ? JSON.parse(after) : undefined);
    const hasMore = rows.length > limit;
    const pageRows = rows.slice(0, limit);
    const last = pageRows[pageRows.length - 1];

    const items: ReedType[] = [];
    const authors: Record<string, User> = {};
    for (const row of pageRows) {
      const reed = await dbService.get<ReedType>('reeds', row.reedID);
      if (!reed) continue;
      items.push(reed);
      if (!authors[reed.userID]) {
        const user = await dbService.get<User>('users', reed.userID);
        if (user) authors[reed.userID] = user;
      }
    }
    return {
      items,
      authors,
      hasMore,
      nextCursor: last ? JSON.stringify({ createdAt: last.createdAt, reedID: last.reedID }) : undefined,
    };
  }

  /**
   * Resend PUBLISH_READY for reeds still awaiting PUBLISH_READY_ACK.
   */
  async announcePublishedReeds(): Promise<void> {
    const pending = await pendingPublicationRepository.getAll();
    if (pending.length === 0) return;

    await serverConnection.connect();
    for (const { reedID } of pending) {
      const reed = await dbService.get<ReedType>('reeds', reedID);
      const broadcast = reed ? !isBlankEcho(reed) : true;
      await serverConnection.publishReady(reedID, { broadcast });
    }
  }
}

export {
  MAX_REED_RAW_CHARS,
  MAX_REED_VISIBLE_CHARS,
  countMarkdownCharacters,
  reedContentWithinLimits,
  stripMarkdown,
} from '$lib/utils/reedContent';

export const reedsService = new ReedsService();

if (typeof window !== 'undefined') {
  onReconnect(() => {
    void reedsService.processUnsignedReeds();
  });
}

const FOLLOW_FEED_LIMIT = 50;
const BROADCAST_KEY = 'broadcastReeds';

/** Drop a reed from the ephemeral broadcast session list (e.g. it arrived via follow). */
export function removeBroadcastReed(reedId: string): void {
  try {
    const raw = sessionStorage.getItem(BROADCAST_KEY);
    if (!raw) return;
    const existing = JSON.parse(raw) as { reeds: ReedType[]; authors: Record<string, User> };
    if (!existing?.reeds?.some((r) => r.id === reedId)) return;
    const updated = {
      reeds: existing.reeds.filter((r) => r.id !== reedId),
      authors: existing.authors,
    };
    sessionStorage.setItem(BROADCAST_KEY, JSON.stringify(updated));
  } catch {
    // ignore corrupt session storage
  }
}

/** Latest reeds from everyone the viewer follows, queried fresh from
 * IndexedDB each call — no session caching, so it always reflects current
 * local state (new follows, newly synced reeds, etc). */
/** A thread shows once, as its head; a part stands alone only when the
 * head isn't held. */
async function collapseThreads(reeds: ReedType[]): Promise<ReedType[]> {
  const shown: ReedType[] = [];
  for (const reed of reeds) {
    if (reed.thread && reed.thread.index > 0 && (await dbService.get<ReedType>('reeds', reed.thread.head))) continue;
    shown.push(reed);
  }
  return shown;
}

export async function getFollowReeds(): Promise<{ reeds: ReedType[]; authors: Record<string, User> }> {
  const following = await dbService.getAll<{ userId: string }>('following');
  const followedSet = new Set(following.map(f => f.userId));
  const reeds = followedSet.size === 0
    ? []
    : await collapseThreads(await dbService.getLatestFromIndex<ReedType>(
        'reeds', 'serverSignature.signedAt', FOLLOW_FEED_LIMIT,
        reed => followedSet.has(reed.userID)
      ));
  const authors: Record<string, User> = {};
  for (const reed of reeds) {
    const authorId = reed.userID;
    if (!authors[authorId]) {
      const user = await dbService.get<User>('users', authorId);
      if (user) authors[authorId] = user;
    }
  }
  return { reeds, authors };
}

const USER_LIST_FEED_LIMIT = 50;

/** Same "latest reeds where userID is in this set" query as the follow
 * feed, scoped to one list's members. No type filtering, mirroring the
 * follow feed. Queried fresh each call — no session caching, since lists
 * are visited far less often than the main feed. */
export async function getUserListReeds(
  userListId: string
): Promise<{ reeds: ReedType[]; authors: Record<string, User>; userList: UserListType | null }> {
  const userList = await userListsRepository.get(userListId);
  if (!userList || userList.memberIds.length === 0) {
    return { reeds: [], authors: {}, userList };
  }
  const memberSet = new Set(userList.memberIds);
  const reeds = await collapseThreads(await dbService.getLatestFromIndex<ReedType>(
    'reeds', 'serverSignature.signedAt', USER_LIST_FEED_LIMIT,
    reed => memberSet.has(reed.userID)
  ));
  const authors: Record<string, User> = {};
  for (const reed of reeds) {
    const authorId = reed.userID;
    if (!authors[authorId]) {
      const user = await dbService.get<User>('users', authorId);
      if (user) authors[authorId] = user;
    }
  }
  return { reeds, authors, userList };
}
