import type * as api from '$lib/types/api';
import type { Verifier } from '$lib/verifiers';

export interface DbMetadata {
  created: number;
  /** UTF-8 byte length of JSON.stringify(payload), excluding __meta__. */
  bytes: number;
}

export type DbWrapper<T> = T & {
  __meta__: DbMetadata;
}

/** UTF-8 size of the durable payload (what abuse detection should watch). */
export function payloadByteLength(data: unknown): number {
  return new TextEncoder().encode(JSON.stringify(data)).byteLength;
}

/** A store's primary key: a single string for most stores, or an array for
 * a store whose keyPath is a compound key (currently just 'reeds':
 * [userID, id] — see storeNames below). */
export type DbKey = string | string[];

export interface DbService {
  init(): Promise<void>;
  put<T extends api.Base>(storeName: string, data: T, verifier: Verifier<T>): Promise<void>;
  get<T extends api.Base>(storeName: string, key: DbKey): Promise<T | null>;
  getMeta(storeName: string, key: DbKey): Promise<DbMetadata | null>;
  delete(storeName: string, key: DbKey): Promise<void>;
  getAll<T extends api.Base>(storeName: string): Promise<T[]>;
  getAllSortedByIndex<T>(storeName: string, indexName: string): Promise<T[]>;
  getLatestFromIndex<T>(storeName: string, indexName: string, limit: number, filter?: (item: T) => boolean): Promise<T[]>;
  getAllByIndex<T>(storeName: string, indexName: string, key: string): Promise<T[]>;
  clear(storeName: string): Promise<void>;
}

export class IndexedDbService implements DbService {
  private db: IDBDatabase | null = null;
  private readonly dbName = 'Syrinx';
  // v8: 'reeds' keyPath became [userID, id] (was 'id' alone) — a reed ID is
  // only unique per author, not globally, so a single-string key let one
  // author's cached reed silently overwrite another's on an ID collision.
  // Wipes local caches on upgrade (see onupgradeneeded below); everything
  // here is peer/server-fetchable, so clients just re-sync on next load.
  // v9: added 'ripples', keyed by hash (content-addressed, globally unique
  // — see specs/ripples/00_design.md's Signing section).
  private readonly version = 9;
  private readonly storeNames = [
    ['following',   'userId'     ],
    ['privateKeys', 'fingerprint'],
    ['publicKeys',  'fingerprint'],
    ['revocations', 'fingerprint'],
    ['tags',        'tagName'    ],
    ['users',       'id'         ],
    ['usersInfo',   'id'         ],
    ['invites',     'id'         ],
    ['reedReplies', 'reedID', 'parentKey'],
    ['ripples',     'hash', 'threadID'],

    // Offline-first
    ['unfollow',           'userId'     ],
    ['unsignedReeds',      'id'         ],
    ['pendingFollows',     'userId'     ],
    ['pendingRevocation',  'fingerprint'],
    ['pendingRemoval',     'reedID'     ],
    ['pendingPublication', 'reedID'     ],
    ['pendingBackups',     'id'         ],
    ['reedRequests',       'requestId'  ],
    ['removedReeds',       'reedID'     ],
    ['removedAccounts',    'userID'     ],
    ['pendingLikes',       'compositeKey'],
    ['pendingUnlike',      'compositeKey'],
    ['likedReeds',         'compositeKey', 'likedAt'],
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

        for (const name of Array.from(db.objectStoreNames)) {
          db.deleteObjectStore(name);
        }

        for (const [storeName, keyPath, ...indexes] of this.storeNames) {
          const store = db.createObjectStore(storeName, { keyPath });
          for (const indexName of indexes) {
            store.createIndex(indexName, indexName, { unique: false });
          }
        }

        // Compound key: a reed ID is only unique per author. Keeps 'userID'
        // and 'serverSignature.timestamp' as regular (non-key) indexes for
        // getReedsByAuthor / deleteReedsByAuthor / recency queries.
        const reedsStore = db.createObjectStore('reeds', { keyPath: ['userID', 'id'] });
        reedsStore.createIndex('userID', 'userID', { unique: false });
        reedsStore.createIndex('serverSignature.timestamp', 'serverSignature.timestamp', { unique: false });
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
        created: Date.now(),
        bytes: payloadByteLength(data),
      }
    };

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.put(wrappedData);

      request.onsuccess = () => resolve(null);
      request.onerror = () => reject(request.error);
    });
  }

  async get<T extends api.Base>(storeName: string, key: DbKey): Promise<T | null> {
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

  /** Read `__meta__` (created/bytes) for a record without unwrapping the payload. */
  async getMeta(storeName: string, key: DbKey): Promise<DbMetadata | null> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const request = store.get(key);

      request.onsuccess = () => {
        const result = request.result as DbWrapper<unknown> | null;
        resolve(result?.__meta__ ?? null);
      };
      request.onerror = () => reject(request.error);
    });
  }

  async delete(storeName: string, key: DbKey): Promise<void> {
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
