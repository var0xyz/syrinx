/**
 * At-rest protection for private key armor.
 *
 * The wrapping key is a non-extractable AES-GCM CryptoKey held in
 * IndexedDB: the browser hands out a handle, never the bytes, so
 * `exportKey` throws and a copied database is inert off-device.
 */

const DB_NAME = 'SyrinxVault';
const STORE = 'wrappingKeys';
const KEY_ID = 'armor-wrapping-key';
const IV_BYTES = 12;

function openVault(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE)) {
        request.result.createObjectStore(STORE);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('Failed to open key vault'));
  });
}

function vaultRequest<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openVault().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const tx = db.transaction(STORE, mode);
        const request = run(tx.objectStore(STORE));
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error ?? new Error('Key vault request failed'));
        tx.oncomplete = () => db.close();
      })
  );
}

/** The stored wrapping key, or null when this device has none yet. */
async function loadWrappingKey(): Promise<CryptoKey | null> {
  const stored = await vaultRequest<CryptoKey | undefined>('readonly', (s) => s.get(KEY_ID));
  return stored ?? null;
}

/**
 * This device's wrapping key, generating and storing it on first use.
 * `extractable: false` is the whole point — do not relax it.
 */
async function ensureWrappingKey(): Promise<CryptoKey> {
  const existing = await loadWrappingKey();
  if (existing) return existing;

  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, [
    'encrypt',
    'decrypt',
  ]);
  await vaultRequest('readwrite', (s) => s.put(key, KEY_ID));
  return key;
}

/** Wrap armor for storage. Output is `base64(iv || ciphertext)`. */
export async function wrapArmor(armor: string): Promise<string> {
  const key = await ensureWrappingKey();
  const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, new TextEncoder().encode(armor))
  );

  const packed = new Uint8Array(iv.length + ciphertext.length);
  packed.set(iv);
  packed.set(ciphertext, iv.length);
  return btoa(String.fromCharCode(...packed));
}

/** Unwrap armor produced by `wrapArmor`. Throws if the key is gone. */
export async function unwrapArmor(wrapped: string): Promise<string> {
  const key = await loadWrappingKey();
  if (!key) {
    throw new Error('No wrapping key on this device; the stored key cannot be unwrapped.');
  }

  const packed = Uint8Array.from(atob(wrapped), (c) => c.charCodeAt(0));
  const plaintext = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: packed.subarray(0, IV_BYTES) },
    key,
    packed.subarray(IV_BYTES)
  );
  return new TextDecoder().decode(plaintext);
}

/** Drop this device's wrapping key, making any stored armor unreadable. */
export async function clearWrappingKey(): Promise<void> {
  await vaultRequest('readwrite', (s) => s.delete(KEY_ID));
}
