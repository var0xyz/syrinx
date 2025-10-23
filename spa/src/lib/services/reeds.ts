/**
 * Reeds Service
 * Handles reed creation, storage, and retrieval
 */

import { apiService as api } from './api';

export interface Reed {
  id: string;
  title: string;
  content: string;
  author: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateReedRequest {
  signature: string;
}

export interface CreateReedResponse {
  reed: Reed;
  success: boolean;
  message?: string;
}

class ReedsService {
  /**
   * Create a new reed
   */
  async createReed(request: CreateReedRequest): Promise<CreateReedResponse> {
    try {
      const response = await api.createReed(request.signature);

      // Store the reed in IndexedDB for offline access
      if (response.reed) {
        await this.storeReedInIndexedDB(response.reed);
      }

      return response;
    } catch (error) {
      console.error('Failed to create reed:', error);
      throw error;
    }
  }

  /**
   * Create a reed with local content storage
   */
  async createReedWithContent(reedContent: string, signature: string): Promise<CreateReedResponse> {
    try {
      console.log('Creating reed with signature:', signature.substring(0, 50) + '...');
      const response = await api.createReed(signature);
      console.log('Server response:', response);

      // Store the reed with content in IndexedDB for offline access
      if (response.reed) {
        // Add the reed content to the reed object before storing
        const reedWithContent = {
          ...response.reed,
          content: reedContent
        };
        console.log('Storing reed in IndexedDB:', reedWithContent);
        await this.storeReedInIndexedDB(reedWithContent);
      } else {
        console.warn('No reed in server response');
      }

      return response;
    } catch (error) {
      console.error('Failed to create reed:', error);
      // Provide more specific error message
      if (error instanceof Error) {
        throw new Error(`Failed to create reed: ${error.message}`);
      }
      throw new Error('Failed to create reed: Unknown error');
    }
  }

  /**
   * Get a specific reed by ID
   */
  async getReed(id: string): Promise<Reed | null> {
    try {
      const response = await api.getReed(id);
      return response;
    } catch (error) {
      console.error('Failed to get reed:', error);
      // Fallback to IndexedDB
      return await this.getReedFromIndexedDB(id);
    }
  }

  /**
   * Store a reed in IndexedDB
   */
  private async storeReedInIndexedDB(reed: Reed): Promise<void> {
    console.log('Storing reed in IndexedDB:', reed);
    try {
      const db = await this.openIndexedDB();
      console.log('Database opened:', db);
      const transaction = db.transaction(['reeds'], 'readwrite');
      console.log('Transaction opened:', transaction);
      const store = transaction.objectStore('reeds');
      console.log('Store opened:', store);
      store.put(reed);
    } catch (error) {
      console.error('Failed to store reed in IndexedDB:', error);
      throw error;
    }
    console.log('Reed stored in IndexedDB:', reed);
  }

  /**
   * Store multiple reeds in IndexedDB
   */
  private async storeReedsInIndexedDB(reeds: Reed[]): Promise<void> {
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
  private async getReedFromIndexedDB(id: string): Promise<Reed | null> {
    try {
      const db = await this.openIndexedDB();
      const transaction = db.transaction(['reeds'], 'readonly');
      const store = transaction.objectStore('reeds');
      const result = store.get(id);
      return result.result || null;
    } catch (error) {
      console.error('Failed to get reed from IndexedDB:', error);
      return null;
    }
  }

  /**
   * Open IndexedDB connection
   */
  private async openIndexedDB(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
      const request = indexedDB.open('syrinx-reeds', 1);

      request.onerror = () => reject(request.error);
      request.onsuccess = () => resolve(request.result);

      request.onupgradeneeded = (event) => {
        const db = (event.target as IDBOpenDBRequest).result;
        if (!db.objectStoreNames.contains('reeds')) {
          const store = db.createObjectStore('reeds', { keyPath: 'id' });
          store.createIndex('createdAt', 'createdAt', { unique: false });
        }
      };
    });
  }

  /**
   * Clear all reeds from IndexedDB
   */
  async clearReedsFromIndexedDB(): Promise<void> {
    try {
      const db = await this.openIndexedDB();
      const transaction = db.transaction(['reeds'], 'readwrite');
      const store = transaction.objectStore('reeds');
      await store.clear();
    } catch (error) {
      console.error('Failed to clear reeds from IndexedDB:', error);
    }
  }
}

export const reedsService = new ReedsService();
