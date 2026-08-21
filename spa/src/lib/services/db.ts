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
  // Bumping `version` must NOT wipe existing stores — see onupgradeneeded
  // below, which is additive: it only creates stores/indexes that don't
  // exist yet on this client, never deletes anything. A prior version of
  // this file deleted every object store on every bump (even when only
  // one new store was added), unconditionally wiping privateKeys,
  // unsignedReeds, and everything else on every client on every feature
  // deploy that touched IndexedDB. There is no "clients resync from the
  // server" fallback for privateKeys/unsignedReeds — the server never
  // holds a copy of either — so that was outright, permanent data loss
  // for every user on every such deploy, not a cache-miss.
  //
  // v8: 'reeds' keyPath became [userID, id] (was 'id' alone) — a reed ID
  // is only unique per author, not globally, so a single-string key let
  // one author's cached reed silently overwrite another's on an ID
  // collision.
  // v9: added 'ripples', keyed by hash (content-addressed, globally
  // unique — see specs/ripples/00_design.md's Signing section).
  // v10: user-key fingerprints became canonical (userID@serverID/fingerprint,
  // was bare) on 'privateKeys', 'publicKeys', 'revocations', and
  // 'pendingRevocation'. keyPath stays 'fingerprint' on all four — only the
  // VALUE shape changed, not the key structure — but an existing bare-keyed
  // row can never match a canonical-keyed lookup again, so it's dead weight.
  // 'publicKeys'/'revocations' are pure server mirrors (the "no resync
  // fallback" warning above does NOT apply to them — only to
  // privateKeys/unsignedReeds); they're cleared here and repopulate on next
  // use via the existing fetch-on-miss paths. 'privateKeys' (irreplaceable
  // local-only key material) and 'pendingRevocation' (unsynced local intent)
  // must NOT be cleared — see migratePrivateKeyFingerprintsV10 in
  // repositories/privateKey.ts and pendingRevocation.ts, called once from
  // app bootstrap after this store upgrade completes.
  //
  // v11: the wire types backing these two stores were renamed
  // (PublicKey.fingerprint -> id; KeyRevocation.fingerprint -> id, the
  // revoked key's own id — see the public_keys backend unification), so
  // keyPath is dropped and recreated on the field the wire type actually
  // carries now, instead of forcing every write site to fabricate a
  // redundant `fingerprint` alias property just to satisfy the old
  // keyPath. Both stores are pure server mirrors (same "no resync
  // fallback" exemption as v10 above) — dropped and recreated rather than
  // re-keying rows in place; they repopulate on next use via the existing
  // fetch-on-miss paths.
  private readonly version = 11;
  private readonly storeNames = [
    ['following',   'userId'     ],
    ['privateKeys', 'fingerprint'],
    ['publicKeys',  'id'         ],
    ['revocations', 'id'         ],
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
        const tx = (event.target as IDBOpenDBRequest).transaction!;

        // v11: 'publicKeys'/'revocations' change keyPath ('fingerprint' ->
        // 'id' on both — see the version comment above). keyPath is
        // immutable on an existing store, so a clean drop-and-recreate is
        // required here, not a plain ensureStore call with the new keyPath
        // (which would silently keep the OLD keyPath on a store that
        // already exists — see the NOTE below). Both are pure server
        // mirrors, safe to drop outright; they repopulate on next use.
        for (const storeName of ['publicKeys', 'revocations']) {
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

        // Compound key: a reed ID is only unique per author. Keeps 'userID'
        // and 'serverSignature.timestamp' as regular (non-key) indexes for
        // getReedsByAuthor / deleteReedsByAuthor / recency queries.
        ensureStore('reeds', ['userID', 'id'], ['userID', 'serverSignature.timestamp']);
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
