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
import { writable } from 'svelte/store';
import { allowUnsigned, verifyReed } from '$lib/verifiers';

// Incremented each time processUnsignedReeds completes successfully
export const unsignedReedsProcessed = writable(0);

export type QueuedReed = { reed: ReedType };

// Receives profile_subscription and request_reed deliveries (explicitly requested content)
export const profileReedQueue = writable<QueuedReed | null>(null);

// Receives new_reed deliveries (catch-up via SYNC_REQUEST and follow broadcasts)
export const newReedQueue = writable<QueuedReed | null>(null);

// Receives broadcast_reed deliveries — ephemeral, NOT stored in IndexedDB
export const broadcastReedQueue = writable<QueuedReed | null>(null);

export function dispatchReedToQueue(reed: ReedType, eventName: string): void {
  const queued: QueuedReed = { reed };
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
   * Create a new reed
   */
  /**
   * Create a new reed. Returns true if published immediately, false if queued for later (server error/offline).
   * Throws if local storage fails.
   */
  async createReed(reed: ReedClass): Promise<boolean> {
    console.log('Storing unsigned reed in IndexedDB:', reed.asObject());
    await dbService.put('unsignedReeds', reed.asObject(), allowUnsigned);

    if (!reed.userSignature?.armor) {
      throw new Error('Reed is missing userSignature');
    }
    try {
      console.log('Getting signature from server...');
      const response = await api.createReed(reed.id, reed.userSignature.armor);
      reed.applyServerResponse(response);
    } catch (error) {
      console.error('Failed to publish reed to server, queued for later:', error);
      return false;
    }

    console.log('Storing signed reed...', reed.asObject());
    await this.storeReed(reed.asObject());
    await dbService.delete('unsignedReeds', reed.id);
    return true;
  }

  /**
   * Process any unsigned reeds that were stored locally but not yet confirmed by the server.
   * Should be called on app startup and when the app comes back online.
   */
  async processUnsignedReeds(): Promise<void> {
    const unsignedReeds = await dbService.getAll<ReedType>('unsignedReeds');
    if (unsignedReeds.length === 0) return;

    for (const reed of unsignedReeds) {
      try {
        if (!reed.userSignature?.armor) {
          console.error('Skipping unsigned reed without userSignature:', reed.id);
          continue;
        }
        console.log('Processing unsigned reed:', reed.id);
        const response = await api.createReed(reed.id, reed.userSignature.armor);
        await this.storeReed({
          ...reed,
          serverSignature: {
            serverID: response.serverID,
            fingerprint: response.fingerprint,
            armor: response.armor,
            timestamp: response.timestamp,
          },
        });
        await dbService.delete('unsignedReeds', reed.id);
      } catch (error) {
        console.error('Failed to process unsigned reed:', reed.id, error);
      }
    }

    unsignedReedsProcessed.update(n => n + 1);
  }

  /**
   * Get a specific reed by ID
   */
  async getReed(userId: string, reedId: string): Promise<ReedType | null> {
    try {
      return await dbService.get<ReedType>('reeds', reedId);
    } catch (error) {
      console.error('Failed to get reed:', error);
      throw error;
    }
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

    serverConnection.fulfillPendingRelayRequest(reed.id, reed);
  }

  async deleteReedsByAuthor(authorId: string): Promise<void> {
    const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', authorId);
    await Promise.all(reeds.map(r => dbService.delete('reeds', r.id)));
  }

  /**
   * Get reeds by author ID
   */
  async getReedsByAuthor(authorId: string): Promise<ReedType[]> {
    try {
      const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'userID', authorId);
      return reeds;
    } catch (error) {
      console.error('Failed to get reeds by author:', error);
      return [];
    }
  }
}

/**
 * Count characters in markdown text, stripping formatting syntax
 * Supports: bold (*text*), italic (_text_), strikethrough (~text~), inline code (`text`), links [text](url)
 * Handles nested formatting and whitespace rules
 */
export function countMarkdownCharacters(text: string): number {
  if (!text) return 0;

  let result = text;

  // Remove links first (keep only the text part)
  result = result.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1');

  // Remove code fences (triple backticks, optional language tag)
  result = result.replace(/```[^\n]*\n?([\s\S]*?)\n```/g, '$1');

  // Remove inline code (single backticks)
  result = result.replace(/`([^`]+)`/g, '$1');

  // Remove strikethrough (single tildes)
  result = result.replace(/~([^~]+)~/g, '$1');

  // Remove italic (single underscores)
  result = result.replace(/_([^_]+)_/g, '$1');

  // Remove bold (single asterisks)
  result = result.replace(/\*([^*]+)\*/g, '$1');

  // Remove # prefix from hashtags (# followed by a non-space character)
  result = result.replace(/(^|\s)#(?=\S)/g, '$1');

  return result.length;
}

/**
 * Strip markdown formatting from text
 */
export function stripMarkdown(text: string): string {
  if (!text) return '';

  let result = text;

  // Remove links first (keep only the text part)
  result = result.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1');

  // Remove inline code (single backticks)
  result = result.replace(/`([^`]+)`/g, '$1');

  // Remove strikethrough (single tildes)
  result = result.replace(/~([^~]+)~/g, '$1');

  // Remove italic (single underscores)
  result = result.replace(/_([^_]+)_/g, '$1');

  // Remove bold (single asterisks)
  result = result.replace(/\*([^*]+)\*/g, '$1');

  return result;
}

export const reedsService = new ReedsService();

const FOLLOWCAST_KEY = 'followcastIds';
const FOLLOWCAST_LIMIT = 50;

export async function initFollowcastIds(): Promise<void> {
  console.log("initFollowcastIds");

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
