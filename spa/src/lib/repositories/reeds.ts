/**
 * Reeds Service
 * Handles reed creation, storage, and retrieval
 */

import { apiService as api } from '../services/api';
import { dbService } from '../services/db';
import { Reed as ReedClass, type ReedType } from '$lib/types/reed';
import { writable } from 'svelte/store';

// Incremented each time processUnsignedReeds completes successfully
export const unsignedReedsProcessed = writable(0);


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
    await dbService.put('unsignedReeds', reed.asObject());

    try {
      console.log('Getting signature from server...');
      const response = await api.createReed(reed.id, reed.userSignature);
      reed.serverAlgorithm = response.algorithm;
      reed.serverSignature = response.signature;
      reed.serverSignedAt = response.signedAt;
    } catch (error) {
      console.error('Failed to publish reed to server, queued for later:', error);
      return false;
    }

    console.log('Storing signed reed...', reed.asObject());
    await this.storeReedInIndexedDB(reed.asObject());
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
        console.log('Processing unsigned reed:', reed.headers.id);
        const response = await api.createReed(reed.headers.id, reed.headers.userSignature);
        const serverHeaders = {
            serverAlgorithm: response.algorithm,
            serverSignature: response.signature,
            serverSignedAt: response.signedAt,
        }
        await this.storeReedInIndexedDB({ ...reed, headers: { ...reed.headers, ...serverHeaders } });
        await dbService.delete('unsignedReeds', reed.headers.id);
      } catch (error) {
        console.error('Failed to process unsigned reed:', reed.headers.id, error);
      }
    }

    unsignedReedsProcessed.update(n => n + 1);
  }

  /**
   * Get a specific reed by ID
   */
  async getReed(userId: string, reedId: string): Promise<ReedType | null> {
    try {
      return await this.getReedFromIndexedDB(reedId);
    } catch (error) {
      console.error('Failed to get reed:', error);
      throw error;
    }
  }

  /**
   * Store a reed in IndexedDB
   */
  private async storeReedInIndexedDB(reed: ReedType): Promise<void> {
    console.log('Storing reed in IndexedDB:', reed);
    try {
      const db = await this.openIndexedDB();
      console.log('Database opened:', db);
      const transaction = db.transaction(['reeds', 'tags'], 'readwrite');
      console.log('Transaction opened:', transaction);

      // Store the reed with its tags array
      const reedsStore = transaction.objectStore('reeds');
      console.log('Reeds store opened:', reedsStore);
      reedsStore.put({ ...reed });

      // Store tags in the tags object store
      if (reed.tags.length > 0) {
        const tagsStore = transaction.objectStore('tags');
        console.log('Tags store opened:', tagsStore);

        for (const tag of reed.tags) {
          let reeds = await this.getTaggedReeds(tagsStore, tag);
          console.log('Tagged reeds:', reeds);
          if (!reeds.includes(reed.headers.id)) {
            reeds = [...reeds, reed.headers.id];
          }
          tagsStore.put({ tagName: tag, reeds });
        }
      }

      transaction.commit();
    } catch (error) {
      console.error('Failed to store reed in IndexedDB:', error);
      throw error;
    }

    console.log('Reed stored in IndexedDB:', reed);
  }

  /**
   * Store multiple reeds in IndexedDB
   */
  private async storeReedsInIndexedDB(reeds: ReedType[]): Promise<void> {
    try {
      const db = await this.openIndexedDB();
      const transaction = db.transaction(['reeds'], 'readwrite');
      const store = transaction.objectStore('reeds');

      for (const reed of reeds) {
        await store.put(reed);
      }
    } catch (error) {
      console.error('Failed to store reeds in IndexedDB:', error);
    }
  }

  /**
   * Get a specific reed from IndexedDB
   */
  private async getReedFromIndexedDB(id: string): Promise<ReedType | null> {
    try {
      const db = await this.openIndexedDB();
      return new Promise((resolve, reject) => {
        const transaction = db.transaction(['reeds'], 'readonly');
        const store = transaction.objectStore('reeds');
        const request = store.get(id);

        request.onsuccess = () => resolve(request.result || null);
        request.onerror = () => reject(request.error);
      });
    } catch (error) {
      console.error('Failed to get reed from IndexedDB:', error);
      return null;
    }
  }

  async deleteReedsByAuthor(authorId: string): Promise<void> {
    const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'headers.author', authorId);
    await Promise.all(reeds.map(r => dbService.delete('reeds', r.headers.id)));
  }

  /**
   * Get reeds by author ID
   */
  async getReedsByAuthor(authorId: string): Promise<ReedType[]> {
    try {
      const reeds = await dbService.getAllByIndex<ReedType>('reeds', 'headers.author', authorId);
      return reeds;
    } catch (error) {
      console.error('Failed to get reeds by author:', error);
      return [];
    }
  }

  /**
   * Open IndexedDB connection
   */
  private async openIndexedDB(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
      const request = indexedDB.open('Syrinx');

      request.onerror = () => reject(request.error);
      request.onsuccess = () => resolve(request.result);

      request.onupgradeneeded = (event) => {
        const db = (event.target as IDBOpenDBRequest).result;
      };
    });
  }

  /**
   * Get tag data from tags store
   */
  private async getTaggedReeds(tagsStore: IDBObjectStore, tagName: string): Promise<string[]> {
    return new Promise((resolve, reject) => {
      const request = tagsStore.get(tagName);
      request.onsuccess = () => resolve(request.result?.reeds || []);
      request.onerror = () => reject(request.error);
    });
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

/**
 * Format timestamp as relative time (just now, X mins/hours/days ago) or absolute date
 */
export function formatRelativeTime(timestamp: string): string {
  if (!timestamp) return '';

  const now = new Date();
  const date = new Date(timestamp);
  const diffMs = now.getTime() - date.getTime();
  const diffSeconds = Math.floor(diffMs / 1000);
  const diffMinutes = Math.floor(diffSeconds / 60);
  const diffHours = Math.floor(diffMinutes / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffSeconds < 15) {
    return 'just now';
  } else if (diffSeconds < 60) {
    return `${diffSeconds} seconds ago`;
  } else if (diffMinutes < 60) {
    return `${diffMinutes} minute${diffMinutes === 1 ? '' : 's'} ago`;
  } else if (diffHours < 24) {
    return `${diffHours} hour${diffHours === 1 ? '' : 's'} ago`;
  } else if (diffDays < 7) {
    return `${diffDays} day${diffDays === 1 ? '' : 's'} ago`;
  } else {
    return formatAbsoluteDate(timestamp);
  }
}

/**
 * Format timestamp as absolute date (MMM DD, YYYY)
 */
export function formatAbsoluteDate(timestamp: string): string {
  if (!timestamp) return '';

  const date = new Date(timestamp);
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
}

/**
 * Format timestamp as absolute date and time (MMM DD, YYYY at HH:MM AM/PM)
 */
export function formatAbsoluteDateTime(timestamp: string): string {
  if (!timestamp) return '';

  const date = new Date(timestamp);
  const dateStr = date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
  const timeStr = date.toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
    hour12: true
  });
  return `${dateStr} at ${timeStr}`;
}

export const reedsService = new ReedsService();
