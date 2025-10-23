import type * as api from '$lib/types/api';
import type * as db from '$lib/types/db';

export interface DbService {
  init(): Promise<void>;
  put<D extends api.Base, T extends db.Base>(storeName: string, data: D): Promise<T>;
  get<T extends db.Base>(storeName: string, key: string): Promise<T | null>;
  delete(storeName: string, key: string): Promise<void>;
  getAll<T extends db.Base>(storeName: string): Promise<T[]>;
  clear(storeName: string): Promise<void>;
}

export class IndexedDbService implements DbService {
  private db: IDBDatabase | null = null;
  private readonly dbName = 'Syrinx';
  private readonly version = 1;
  private readonly storeNames = [
    ['privateKeys', 'fingerprint'],
    ['publicKeys',  'fingerprint'],
    ['reeds',       'id'         ],
    ['userReeds',   'userID'     ],
    ['users',       'id'         ],
  ];

  async init(): Promise<void> {
    if (this.db) return;

    return new Promise((resolve, reject) => {
      const request = indexedDB.open(this.dbName, this.version);

      request.onerror = () => reject(request.error);
      request.onsuccess = () => {
        this.db = request.result;
        resolve();
      };

      request.onupgradeneeded = (event) => {
        const db = (event.target as IDBOpenDBRequest).result;

        // Create object stores for each store name
        this.storeNames.forEach(([storeName, keyPath]) => {
            db.createObjectStore(storeName, { keyPath });
        });
      };
    });
  }

  async put<D extends api.Base, T extends db.Base>(storeName: string, data: D): Promise<T> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    console.log('Putting data into store:', storeName, data);

    // Add metadata with timestamp
    const entry = {
      ...data,
      __meta__: {
        createdAt: new Date().toISOString()
      }
    };

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.put(entry);

      request.onsuccess = () => resolve(entry as unknown as T);
      request.onerror = () => reject(request.error);
    });
  }

  async get<T extends db.Base>(storeName: string, key: string): Promise<T | null> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const request = store.get(key);

      request.onsuccess = () => resolve(request.result as T | null);
      request.onerror = () => reject(request.error);
    });
  }

  async delete(storeName: string, key: string): Promise<void> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.delete(key);

      request.onsuccess = () => resolve();
      request.onerror = () => reject(request.error);
    });
  }

  async getAll<T>(storeName: string): Promise<T[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const request = store.getAll();

      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  }

  async clear(storeName: string): Promise<void> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.clear();

      request.onsuccess = () => resolve();
      request.onerror = () => reject(request.error);
    });
  }
}

export const dbService = new IndexedDbService();
