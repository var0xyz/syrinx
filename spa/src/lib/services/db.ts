import type * as api from '$lib/types/api';
import type { Verifier } from '$lib/verifiers';

export interface DbMetadata {
  created: number;
}

export type DbWrapper<T> = T & {
  __meta__: DbMetadata;
}

export interface DbService {
  init(): Promise<void>;
  put<T extends api.Base>(storeName: string, data: T, verifier: Verifier<T>): Promise<void>;
  get<T extends api.Base>(storeName: string, key: string): Promise<T | null>;
  delete(storeName: string, key: string): Promise<void>;
  getAll<T extends api.Base>(storeName: string): Promise<T[]>;
  getAllSortedByIndex<T>(storeName: string, indexName: string): Promise<T[]>;
  getLatestFromIndex<T>(storeName: string, indexName: string, limit: number, filter?: (item: T) => boolean): Promise<T[]>;
  clear(storeName: string): Promise<void>;
}

export class IndexedDbService implements DbService {
  private db: IDBDatabase | null = null;
  private readonly dbName = 'Syrinx';
  // v21: reed JSON flattened — headers.id/author → id/userID (signatures 08).
  // v20: reeds index server.timestamp → serverSignature.timestamp (signatures 08).
  // v19: drop pendingAccountRemoval — account deletion is online-only (09).
  // removedAccounts remains for peer tombstones.
  private readonly version = 21;
  private readonly storeNames = [
    ['following',   'userId'     ],
    ['privateKeys', 'fingerprint'],
    ['publicKeys',  'fingerprint'],
    ['revocations', 'fingerprint'],
    ['reeds',       'id', 'userID', 'serverSignature.timestamp'],
    ['tags',        'tagName'    ],
    ['users',       'id'         ],

    // Offline-first
    ['unfollow',          'userId'     ],
    ['unsignedReeds',     'id' ],
    ['pendingFollows',    'userId'     ],
    ['pendingRevocation', 'fingerprint'],
    ['pendingRemoval',    'reedID'     ],
    ['removedReeds',      'reedID'     ],
    ['removedAccounts',   'userID'     ],
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
        const tx = (event.target as IDBOpenDBRequest).transaction!;
        const oldVersion = event.oldVersion;

        // KeyPath change headers.id → id: drop reed stores so forEach recreates them.
        if (oldVersion > 0 && oldVersion < 21) {
          for (const name of ['reeds', 'unsignedReeds'] as const) {
            if (db.objectStoreNames.contains(name)) {
              db.deleteObjectStore(name);
            }
          }
        }

        this.storeNames.forEach(([storeName, keyPath, ...indexes]) => {
          const store = db.objectStoreNames.contains(storeName)
            ? tx.objectStore(storeName)
            : db.createObjectStore(storeName, { keyPath });

          for (const indexName of indexes) {
            if (!store.indexNames.contains(indexName)) {
              store.createIndex(indexName, indexName, { unique: false });
            }
          }
        });

        if (oldVersion > 0 && oldVersion < 16 && db.objectStoreNames.contains('publicKeys')) {
          tx.objectStore('publicKeys').clear();
        }
        if (oldVersion > 0 && oldVersion < 19 && db.objectStoreNames.contains('pendingAccountRemoval')) {
          db.deleteObjectStore('pendingAccountRemoval');
        }
        if (oldVersion > 0 && oldVersion < 20 && db.objectStoreNames.contains('reeds')) {
          const reedsStore = tx.objectStore('reeds');
          if (reedsStore.indexNames.contains('server.timestamp')) {
            reedsStore.deleteIndex('server.timestamp');
          }
        }
      };
    });
  }

  async put<T extends api.Base>(
    storeName: string,
    data: T,
    verifier: Verifier<T>
  ): Promise<void> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    const ok = await verifier(data);
    if (!ok) {
      throw new Error(`Refusing to store in ${storeName}: verification failed`);
    }

    const wrappedData: DbWrapper<T> = {
      ...data,
      __meta__: {
        created: Date.now()
      }
    };

    console.log('Putting data into store:', storeName, wrappedData);

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.put(wrappedData);

      request.onsuccess = () => resolve(null);
      request.onerror = () => reject(request.error);
    });
  }

  async get<T extends api.Base>(storeName: string, key: string): Promise<T | null> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const request = store.get(key);

      request.onsuccess = () => {
        const result = request.result as DbWrapper<T> | null;
        if (!result) {
          resolve(null);
          return;
        }
        const { __meta__, ...data } = result;
        resolve(data as unknown as T);
      };
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

      request.onsuccess = () => {
        const results = request.result as DbWrapper<T>[];
        const unwrapped = results.map(item => {
          const { __meta__, ...data } = item;
          return data as T;
        });
        resolve(unwrapped);
      };
      request.onerror = () => reject(request.error);
    });
  }

  async getLatestFromIndex<T>(storeName: string, indexName: string, limit: number, filter?: (item: T) => boolean): Promise<T[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const index = store.index(indexName);
      const request = index.openCursor(null, 'prev');
      const results: T[] = [];

      request.onsuccess = () => {
        const cursor = request.result;
        if (!cursor || results.length >= limit) {
          resolve(results);
          return;
        }
        const { __meta__, ...data } = cursor.value;
        const item = data as T;
        if (!filter || filter(item)) {
          results.push(item);
        }
        cursor.continue();
      };
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

  async getAllByIndex<T>(storeName: string, indexName: string, key: string): Promise<T[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const index = store.index(indexName);
      const request = index.getAll(key);

      request.onsuccess = () => {
        const results = request.result as DbWrapper<T>[];
        const unwrapped = results.map(item => {
          const { __meta__, ...data } = item;
          return data as T;
        });
        resolve(unwrapped);
      };
      request.onerror = () => reject(request.error);
    });
  }

  async getAllSortedByIndex<T>(storeName: string, indexName: string): Promise<T[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const index = store.index(indexName);
      const request = index.openCursor();
      const results: T[] = [];

      request.onsuccess = () => {
        const cursor = request.result;
        if (cursor) {
          const { __meta__, ...data } = cursor.value;
          results.push(data as T);
          cursor.continue();
        } else {
          resolve(results);
        }
      };
      request.onerror = () => reject(request.error);
    });
  }

  /**
   * Get all table (object store) names from the database
   */
  async getTableNames(): Promise<string[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return Array.from(this.db.objectStoreNames);
  }

  /**
   * Delete the entire IndexedDB database
   */
  async deleteDatabase(): Promise<void> {
    // Close existing connection if open
    if (this.db) {
      this.db.close();
      this.db = null;
    }

    return new Promise<void>((resolve, reject) => {
      const deleteRequest = indexedDB.deleteDatabase(this.dbName);

      deleteRequest.onsuccess = () => {
        console.log('IndexedDB database deleted successfully');
        resolve();
      };

      deleteRequest.onerror = () => {
        console.error('Failed to delete IndexedDB database:', deleteRequest.error);
        reject(deleteRequest.error);
      };

      deleteRequest.onblocked = () => {
        console.warn('IndexedDB deletion blocked - database may be in use');
        // Still resolve to allow continuation
        resolve();
      };
    });
  }
}

export const dbService = new IndexedDbService();
