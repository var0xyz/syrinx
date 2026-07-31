/**
 * Reeds Service
 * Handles reed creation, storage, and retrieval
 */

import { apiService as api } from '../services/api';
import { dbService } from '../services/db';
import { publicKeyRepository } from './publicKey';
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

// Incremented each time processUnsignedReeds completes successfully
export const unsignedReedsProcessed = writable(0);

export type QueuedReed = {
  reed: ReedType;
  /** Server-sourced display label for ephemeral broadcast deliveries. */
  username?: string;
};

// Receives profile_subscription and request_reed deliveries (explicitly requested content)
export const profileReedQueue = writable<QueuedReed | null>(null);

// Receives new_reed deliveries (catch-up via SYNC_REQUEST and follow broadcasts)
export const newReedQueue = writable<QueuedReed | null>(null);

// Receives broadcast_reed deliveries — ephemeral, NOT stored in IndexedDB
export const broadcastReedQueue = writable<QueuedReed | null>(null);

export function dispatchReedToQueue(
  reed: ReedType,
  eventName: string,
  username?: string
): void {
  const queued: QueuedReed = { reed, username };
  if (eventName === 'new_reed') {
    newReedQueue.set(queued);
  } else if (eventName === 'broadcast_reed') {
    broadcastReedQueue.set(queued);
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
      const response = await api.createReed(reed.id, armor, {
        content: reed.content,
        echoing: reed.echoing,
        replying: reed.replying,
      });
      let published: ReedType;
      if (reed instanceof ReedClass) {
        reed.applyServerResponse(response);
        published = reed.asObject();
      } else {
        published = {
          ...reed,
          serverSignature: {
            serverID: response.serverID,
            fingerprint: response.fingerprint,
            armor: response.armor,
            timestamp: response.timestamp,
          },
        };
      }
      await this.storeReed(published);
      await pendingPublicationRepository.put(published.id);
      await serverConnection.connect();
      const broadcast = !isBlankEcho(published);
      await serverConnection.publishReady(published.id, { broadcast });
      await dbService.delete('unsignedReeds', reed.id);
      unsignedReedsProcessed.update((n) => n + 1);
      return true;
    } catch (error) {
      console.error('Failed to publish reed to server, queued for later:', error);
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
   * Get a specific reed by ID (published / countersigned only).
   */
  async getReed(userId: string, reedId: string): Promise<ReedType | null> {
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
   */
  async storeReed(reed: ReedType): Promise<void> {
    // Ensure author key is cached (verifyReed fetches; put attests).
    if (reed.userSignature?.fingerprint && reed.userID) {
      try {
        const key = await api.getPublicKey(reed.userID, reed.userSignature.fingerprint);
        await publicKeyRepository.put(key);
      } catch (error) {
        console.error('Failed to cache author public key before reed store:', error);
      }
    }

    await dbService.put('reeds', reed, verifyReed);

    if (reed.tags?.length > 0) {
      for (const tag of reed.tags) {
        const existing = await dbService.get<{ tagName: string; reeds: string[] }>('tags', tag);
        const reeds = existing?.reeds?.includes(reed.id)
          ? existing.reeds
          : [...(existing?.reeds ?? []), reed.id];
        await dbService.put('tags', { tagName: tag, reeds }, allowUnsigned);
      }
    }
  }

  async deleteReedsByAuthor(authorId: string): Promise<void> {
    const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', authorId);
    await Promise.all(reeds.map(r => dbService.delete('reeds', r.id)));
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

const FOLLOWCAST_KEY = 'followcastIds';
const FOLLOWCAST_LIMIT = 50;
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

export async function initFollowcastIds(): Promise<void> {
  if (sessionStorage.getItem(FOLLOWCAST_KEY) !== null) return;
  const following = await dbService.getAll<{ userId: string }>('following');
  if (following.length === 0) return;
  const followedSet = new Set(following.map(f => f.userId));
  const reeds = await dbService.getLatestFromIndex<ReedType>(
    'reeds', 'serverSignature.timestamp', FOLLOWCAST_LIMIT,
    reed => followedSet.has(reed.userID)
  );
  sessionStorage.setItem(FOLLOWCAST_KEY, JSON.stringify(reeds.map(r => r.id)));
}

export function prependFollowcastId(reedId: string): void {
  let ids: string[] = [];
  try {
    ids = JSON.parse(sessionStorage.getItem(FOLLOWCAST_KEY) ?? '[]');
  } catch {
    // ignore
  }
  if (!ids.includes(reedId)) {
    ids = [reedId, ...ids].slice(0, FOLLOWCAST_LIMIT);
    sessionStorage.setItem(FOLLOWCAST_KEY, JSON.stringify(ids));
  }
}

export async function getFollowcastReeds(): Promise<{ reeds: ReedType[]; authors: Record<string, User> }> {
  let ids: string[] = [];
  try {
    ids = JSON.parse(sessionStorage.getItem(FOLLOWCAST_KEY) ?? '[]');
  } catch {
    // ignore
  }
  const reeds: ReedType[] = [];
  for (const id of ids) {
    const reed = await dbService.get<ReedType>('reeds', id);
    if (reed) reeds.push(reed);
  }
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
