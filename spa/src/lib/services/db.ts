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
 * a store whose keyPath is a compound key. */
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
  // Bumping `version` must NOT wipe existing stores — see onupgradeneeded
  // below, which is additive: it only creates stores/indexes that don't
  // exist yet on this client, never deletes anything, except for stores
  // undergoing a keyPath change (IndexedDB keyPaths are immutable, so those
  // must be dropped and recreated — see the drop loop below). Pre-launch,
  // so dropped stores' data loss is acceptable rather than migrated.
  private readonly version = 14;
  private readonly storeNames = [
    ['following',   'userId'     ],
    ['privateKeys', 'keyId'      ],
    ['publicKeys',  'id'         ],
    ['revocations', 'id'         ],
    ['tags',        'tagName'    ],
    ['users',       'id'         ],
    ['usersInfo',   'id'         ],
    ['invites',     'id'         ],
    ['reedReplies', 'reedID', 'parentReedID'],
    ['ripples',     'hash', 'threadID'],

    // Offline-first
    ['unfollow',           'userId'     ],
    ['unsignedReeds',      'id'         ],
    ['pendingFollows',     'userId'     ],
    ['pendingRevocation',  'keyId'      ],
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
        const tx = (event.target as IDBOpenDBRequest).transaction!;

        // keyPath is immutable on an existing store, so a version bump that
        // changes a store's keyPath must drop and recreate it — a plain
        // ensureStore call would silently keep the OLD keyPath on a store
        // that already exists (see the NOTE below). Pre-launch project, no
        // production data to preserve, so dropped stores are just cleared,
        // not migrated in place — including privateKeys/pendingRevocation
        // here for the fingerprint -> keyId rename.
        for (const storeName of ['publicKeys', 'revocations', 'reeds', 'privateKeys', 'pendingRevocation']) {
          if (db.objectStoreNames.contains(storeName)) {
            db.deleteObjectStore(storeName);
          }
        }

        // NOTE: keyPath is immutable on an existing store — IndexedDB has
        // no in-place "change the key" API. If a future change needs a
        // different keyPath for a store that already shipped (like v8's
        // 'reeds' change, or v11's 'publicKeys'/'revocations' change
        // above), this helper's `contains(storeName)` branch will keep the
        // OLD keyPath silently unless the store was explicitly dropped
        // first.
        const ensureStore = (
          storeName: string,
          keyPath: string | string[],
          indexes: string[]
        ) => {
          const store = db.objectStoreNames.contains(storeName)
            ? tx.objectStore(storeName)
            : db.createObjectStore(storeName, { keyPath });
          for (const indexName of indexes) {
            if (!store.indexNames.contains(indexName)) {
              store.createIndex(indexName, indexName, { unique: false });
            }
          }
        };

        for (const [storeName, keyPath, ...indexes] of this.storeNames) {
          ensureStore(storeName, keyPath, indexes);
        }

        // Reed ids are canonical (globally unique) as of v12 — dropped above.
        ensureStore('reeds', 'id', ['userID', 'serverSignature.timestamp']);
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
