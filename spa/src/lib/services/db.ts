import type * as api from '$lib/types/api';

export interface DbService {
  init(): Promise<void>;
  put<T extends api.Base>(storeName: string, data: T): Promise<void>;
  get<T extends api.Base>(storeName: string, key: string): Promise<T | null>;
  delete(storeName: string, key: string): Promise<void>;
  getAll<T extends api.Base>(storeName: string): Promise<T[]>;
  clear(storeName: string): Promise<void>;
}

export class IndexedDbService implements DbService {
  private db: IDBDatabase | null = null;
  private readonly dbName = 'Syrinx';
  private readonly version = 1;
  private readonly storeNames = [
    ['privateKeys', 'fingerprint'],
    ['publicKeys',  'fingerprint'],
    ['revokedKeys', 'fingerprint'],
    ['reeds',       'headers.id', 'headers.author'],
    ['userReeds',   'userID'     ],
    ['users',       'id'         ],
    ['tags',        'tagName'    ],
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
        this.storeNames.forEach((storeDef) => {
            const [storeName, keyPath] = storeDef;
            const store = db.createObjectStore(storeName, { keyPath });

            if (storeDef.length === 3) {
              const indexName = storeDef[2];
              store.createIndex(indexName, indexName, { unique: false });
            }
        });
      };
    });
  }

  async put<T extends api.Base>(storeName: string, data: T): Promise<void> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    console.log('Putting data into store:', storeName, data);

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readwrite');
      const store = transaction.objectStore(storeName);
      const request = store.put(data);

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

  async getAllByIndex<T>(storeName: string, indexName: string, key: string): Promise<T[]> {
    await this.init();
    if (!this.db) throw new Error('Database not initialized');

    return new Promise((resolve, reject) => {
      const transaction = this.db!.transaction([storeName], 'readonly');
      const store = transaction.objectStore(storeName);
      const index = store.index(indexName);
      const request = index.getAll(key);

      request.onsuccess = () => resolve(request.result);
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
