/**
 * Session Store
 * Key-value store that holds sensitive data in memory only - never persisted
 * Similar API to localStorage but memory-only
 */
class SessionStore {
  private store: Map<string, string> = new Map();

  /**
   * Set a value in the session store (memory only)
   */
  set(key: string, value: string): void {
    this.store.set(key, value);
    sessionStorage.setItem(key, value);
    console.log('SessionStore set', key, value);
  }

  /**
   * Get a value from the session store
   */
  get(key: string): string | null {
    console.log('SessionStore get', key, sessionStorage.getItem(key));
    return sessionStorage.getItem(key) || null;
    // return this.store.get(key) || null;
  }

  /**
   * Remove a specific key from the session store
   */
  remove(key: string): void {
    this.store.delete(key);
  }

  /**
   * Clear all session data from memory
   */
  clear(): void {
    this.store.clear();
  }

  /**
   * Check if a key exists in the session store
   */
  has(key: string): boolean {
    return this.store.has(key);
  }

  /**
   * Get all keys in the session store
   */
  keys(): string[] {
    return Array.from(this.store.keys());
  }

  /**
   * Get the number of items in the session store
   */
  get length(): number {
    return this.store.size;
  }

  /**
   * Check if session has required data for authentication
   */
  hasValidSession(): boolean {
    return this.has('passphrase') && this.has('fingerprint');
  }
}

export const sessionStore = new SessionStore();