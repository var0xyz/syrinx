import type * as api from '$lib/types/api';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { apiService, canonicalKeyId } from './api';
import { authService } from './auth';
import { ensureDeviceId } from './deviceId';
import { localStorageService } from './localstorage';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { revocationRepository } from '$lib/repositories/revocation';
import { removedAccountsRepository } from '$lib/repositories/removedAccounts';
import { removedReedsRepository } from '$lib/repositories/removedReeds';
import { reedsService } from '$lib/repositories/reeds';
import { userRepository } from '$lib/repositories/user';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { allowUnsigned } from '$lib/verifiers';
import type { PrivateKey } from '$lib/repositories/privateKey';
import type { ReedType } from '$lib/types/reed';

export type BackupTable = {
  name: string;
  items?: unknown[];
};

export type BackupPayload = {
  timestamp?: number;
  origin?: string;
  localStorage?: Record<string, string>;
  indexedDB?: {
    name?: string;
    tables?: BackupTable[];
  };
};

const FULL_BACKUP_FILENAME_RE = /^syrinx-.+\.sxb\.gpg$/i;
const IDENTITY_BACKUP_FILENAME_RE = /^syrinx-.+\.sxi\.gpg$/i;

/** Strip spurious suffixes some browsers append to unrecognized extensions. */
export function normalizeExportFilename(name: string): string {
  let normalized = name.trim();
  while (/\.(com|download)$/i.test(normalized)) {
    normalized = normalized.replace(/\.(com|download)$/i, '');
  }
  return normalized;
}

export function isFullBackupFilename(name: string): boolean {
  return FULL_BACKUP_FILENAME_RE.test(normalizeExportFilename(name));
}

export function isIdentityBackupFilename(name: string): boolean {
  return IDENTITY_BACKUP_FILENAME_RE.test(normalizeExportFilename(name));
}

/** Full (`.sxb.gpg`) or identity (`.sxi.gpg`) Syrinx export. */
export function isBackupFilename(name: string): boolean {
  const n = normalizeExportFilename(name);
  return isFullBackupFilename(n) || isIdentityBackupFilename(n);
}

export type BackupSaveKind = 'full' | 'identity';

const SAVE_FILE_TYPES: Record<BackupSaveKind, { description: string; extensions: string[] }> = {
  full: {
    description: 'Syrinx full backup',
    // Include `.gpg` — browsers recognize it; avoids spurious `.com` suffixes.
    extensions: ['.gpg', '.sxb.gpg'],
  },
  identity: {
    description: 'Syrinx identity backup',
    extensions: ['.gpg', '.sxi.gpg'],
  },
};

async function compressGzip(data: Uint8Array): Promise<Uint8Array> {
  const stream = new CompressionStream('gzip');
  const writer = stream.writable.getWriter();
  writer.write(data as BufferSource);
  writer.close();

  const chunks: Uint8Array[] = [];
  const reader = stream.readable.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const total = chunks.reduce((sum, c) => sum + c.length, 0);
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.length;
  }
  return result;
}

/** Gzip-compress a backup payload's JSON serialization. */
export async function compressBackupPayload(payload: BackupPayload): Promise<Uint8Array> {
  return compressGzip(new TextEncoder().encode(JSON.stringify(payload)));
}

/**
 * Encrypt and save a compressed backup payload to disk, prompting for a
 * file location via File System Access API where available. Returns false
 * if the user cancelled the save dialog.
 */
export async function encryptAndSaveBackup(
  compressed: Uint8Array,
  password: string,
  filename: string,
  kind: BackupSaveKind = 'full'
): Promise<boolean> {
  const encryptedData = await cryptoService.encryptBackup(compressed, password);
  const fileType = SAVE_FILE_TYPES[kind];

  if ('showSaveFilePicker' in window) {
    try {
      const fileHandle = await (window as any).showSaveFilePicker({
        suggestedName: filename,
        types: [{
          description: fileType.description,
          accept: { 'application/octet-stream': fileType.extensions },
        }],
      });
      const writable = await fileHandle.createWritable();
      await writable.write(new Blob([encryptedData.slice()], { type: 'application/octet-stream' }));
      await writable.close();
      return true;
    } catch (error: any) {
      if (error.name === 'AbortError') return false;
      throw error;
    }
  }

  const blob = new Blob([encryptedData.slice()], { type: 'application/octet-stream' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
  return true;
}

/**
 * Keys-only identity backup: active private + public key and session
 * localStorage markers. Profile comes from server bootstrap on import
 * ([accountRecovery.ts](../account_recovery/ — slice 04)).
 */
export async function buildKeyBackupPayload(): Promise<BackupPayload> {
  const userId = localStorage.getItem('userId');
  const fingerprint = authService.getActiveKeyFingerprint();
  if (!userId || !fingerprint) {
    throw new Error('No active identity to back up.');
  }

  const allLocalStorage = localStorageService.getAll();
  const IDENTITY_KEYS = ['userId', 'keyFingerprint', 'keyPassphrase', 'serverId', 'serverName'];
  const localStorageSubset: Record<string, string> = {};
  for (const key of IDENTITY_KEYS) {
    if (allLocalStorage[key] !== undefined) localStorageSubset[key] = allLocalStorage[key];
  }

  const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
  if (!privateKey) {
    throw new Error('Active private key not found locally.');
  }

  let publicKey = await publicKeyRepository.getPublicKey(fingerprint);
  if (!publicKey) {
    publicKey = await apiService.getPublicKey(canonicalKeyId(userId, fingerprint));
  }

  return {
    timestamp: Date.now(),
    origin: window.location.origin,
    localStorage: localStorageSubset,
    indexedDB: {
      name: 'Syrinx',
      tables: [
        // restoreItem's privateKeys/publicKeys cases atob() the armor on the
        // way back in (matching the full-backup export below) — repository
        // armor is stored raw, so it must be base64-wrapped here too.
        { name: 'privateKeys', items: [{ ...privateKey, armor: btoa(privateKey.armor) }] },
        { name: 'publicKeys', items: [{ ...publicKey, armor: btoa(publicKey.armor) }] },
      ],
    },
  };
}

async function decompressGzip(data: Uint8Array): Promise<Uint8Array> {
  const stream = new DecompressionStream('gzip');
  const writer = stream.writable.getWriter();
  writer.write(data as BufferSource);
  writer.close();

  const chunks: Uint8Array[] = [];
  const reader = stream.readable.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const total = chunks.reduce((sum, c) => sum + c.length, 0);
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.length;
  }
  return result;
}

/**
 * Decrypt and parse a backup file in memory. Does not write storage.
 */
export async function decryptBackupFile(file: File, password: string): Promise<BackupPayload> {
  const fileBytes = new Uint8Array(await file.arrayBuffer());

  let decrypted: Uint8Array;
  try {
    decrypted = await cryptoService.decryptBackup(fileBytes, password);
  } catch {
    throw new Error(
      'Failed to decrypt backup. Check that the password is correct and the file is not corrupted.'
    );
  }

  let decompressed: Uint8Array;
  try {
    decompressed = await decompressGzip(decrypted);
  } catch {
    throw new Error('Failed to decompress backup. The file may be corrupted.');
  }

  try {
    return JSON.parse(new TextDecoder().decode(decompressed)) as BackupPayload;
  } catch {
    throw new Error('Invalid backup file: could not parse contents.');
  }
}

/**
 * Extract the countersigned profile for the backup's userId from IndexedDB users.
 */
export function extractProfile(backup: BackupPayload): api.User {
  const ls = backup.localStorage ?? {};
  const userId = ls['userId'];
  if (!userId) {
    throw new Error('Invalid backup file: missing userId.');
  }

  const usersTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'users');
  const profile = (usersTable?.items ?? []).find(
    (item) => item && typeof item === 'object' && (item as api.User).id === userId
  ) as api.User | undefined;

  if (
    !profile?.id ||
    !profile.username ||
    !profile.userSignature?.id ||
    !profile.serverSignature
  ) {
    throw new Error('Invalid backup file: missing countersigned profile.');
  }

  return profile;
}

/**
 * Validate keys-only identity backup (`.sxi.gpg`). Profile must not be present.
 */
export function assertIdentityBackupKeys(backup: BackupPayload): void {
  const ls = backup.localStorage ?? {};
  const userId = ls['userId'];
  const keyFingerprint = ls['keyFingerprint'];
  const keyPassphrase = ls['keyPassphrase'];
  const serverId = ls['serverId'];

  const privateKeysTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'privateKeys');
  const privateKeyEntry = (privateKeysTable?.items ?? []).find(
    (k) => k && typeof k === 'object' && backupKeyItemId(k) === keyFingerprint
  ) as { armor?: string } | undefined;

  const publicKeysTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'publicKeys');
  const publicKeyEntry = (publicKeysTable?.items ?? []).find(
    (k) => k && typeof k === 'object' && backupKeyItemId(k) === keyFingerprint
  );

  const usersTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'users');
  if ((usersTable?.items ?? []).length > 0) {
    throw new Error('Invalid identity backup: profile must not be included.');
  }

  if (!userId || !keyFingerprint || !keyPassphrase || !serverId || !privateKeyEntry?.armor) {
    throw new Error('Invalid identity backup: missing required key material.');
  }
  if (!publicKeyEntry) {
    throw new Error('Invalid identity backup: missing active public key.');
  }
}

/** True when payload has keys but no embedded profile (identity export). */
export function isIdentityBackupPayload(backup: BackupPayload): boolean {
  const usersTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'users');
  return (usersTable?.items ?? []).length === 0;
}

/**
 * Write keys + session markers from an identity backup. Does not write profile.
 */
export async function writeIdentityKeysBackup(backup: BackupPayload): Promise<void> {
  assertIdentityBackupKeys(backup);

  const preservedDeviceId =
    typeof localStorage !== 'undefined' ? localStorage.getItem('deviceId') : null;

  const ls = backup.localStorage ?? {};
  for (const [key, value] of Object.entries(ls)) {
    if (key === 'deviceId') continue;
    localStorage.setItem(key, value);
  }

  if (preservedDeviceId) {
    localStorage.setItem('deviceId', preservedDeviceId);
  } else {
    ensureDeviceId();
  }

  await dbService.init();

  const tables = backup.indexedDB?.tables ?? [];
  for (const tableName of ['publicKeys', 'privateKeys'] as const) {
    const table = tables.find((t) => t.name === tableName);
    for (const item of table?.items ?? []) {
      await restoreItem(tableName, item);
    }
  }
}

/**
 * Validate that the backup carries identity material needed for a session.
 */
export function assertBackupIdentity(backup: BackupPayload): void {
  const ls = backup.localStorage ?? {};
  const userId = ls['userId'];
  const keyFingerprint = ls['keyFingerprint'];
  const keyPassphrase = ls['keyPassphrase'];

  const privateKeysTable = (backup.indexedDB?.tables ?? []).find((t) => t.name === 'privateKeys');
  const privateKeyEntry = (privateKeysTable?.items ?? []).find(
    (k) => k && typeof k === 'object' && backupKeyItemId(k) === keyFingerprint
  ) as { armor?: string } | undefined;

  if (!userId || !keyFingerprint || !keyPassphrase || !privateKeyEntry?.armor) {
    throw new Error('Invalid backup file: missing required identity data.');
  }
}

/**
 * A backup's privateKeys/publicKeys item can carry either `id` (the Go
 * root-key exporter's identityPrivateKeyItem/Key wire shapes) or
 * `fingerprint` (the SPA's own export, which spreads an IndexedDB-shaped
 * record where `fingerprint` is a keyPath alias of `id` — see
 * verifiers/index.ts's write path). Match either.
 */
export function backupKeyItemId(item: object): string | undefined {
  return (item as { id?: string; fingerprint?: string }).id
    ?? (item as { id?: string; fingerprint?: string }).fingerprint;
}

async function restoreItem(storeName: string, item: unknown): Promise<void> {
  switch (storeName) {
    case 'publicKeys': {
      const key = item as api.PublicKey;
      await publicKeyRepository.put({ ...key, armor: atob(key.armor) });
      return;
    }
    case 'revocations':
      await revocationRepository.put(item as api.KeyRevocation);
      return;
    case 'users': {
      const user = item as api.User & { __meta__?: { deleted?: boolean } };
      if ((user as any)?.__meta__?.deleted || (user as any)?.deleted) {
        await userRepository.writeTombstone(user.id);
        return;
      }
      await userRepository.put(user);
      return;
    }
    case 'reeds':
      await reedsService.storeReed(item as ReedType);
      return;
    case 'removedReeds':
      await removedReedsRepository.put(item as api.ReedRemoval);
      return;
    case 'removedAccounts':
      await removedAccountsRepository.put(item as api.AccountRemoval);
      return;
    case 'echoCounts':
    case 'replyCounts':
      return;
    case 'privateKeys': {
      const key = item as PrivateKey;
      const id = backupKeyItemId(key as unknown as object) ?? key.fingerprint;
      await privateKeyRepository.put(id, atob(key.armor));
      if (key.revoked) {
        await privateKeyRepository.setRevoked(id);
      }
      return;
    }
    default:
      await dbService.put(storeName, item as api.Base, allowUnsigned);
  }
}

/**
 * Write backup contents to localStorage and IndexedDB.
 * Signed stores go through repository puts (real verifiers).
 */
export async function writeBackup(backup: BackupPayload): Promise<void> {
  const preservedDeviceId =
    typeof localStorage !== 'undefined' ? localStorage.getItem('deviceId') : null;

  const ls = backup.localStorage ?? {};
  for (const [key, value] of Object.entries(ls)) {
    if (key === 'deviceId') continue;
    localStorage.setItem(key, value);
  }

  if (preservedDeviceId) {
    localStorage.setItem('deviceId', preservedDeviceId);
  } else {
    ensureDeviceId();
  }

  await dbService.init();

  // Restore public keys before users/reeds/revocations so verifiers can resolve armor.
  const tables = backup.indexedDB?.tables ?? [];
  const ordered = [
    ...tables.filter((t) => t.name === 'publicKeys'),
    ...tables.filter((t) => t.name === 'privateKeys'),
    ...tables.filter((t) => t.name !== 'publicKeys' && t.name !== 'privateKeys'),
  ];

  for (const table of ordered) {
    for (const item of table.items ?? []) {
      try {
        await restoreItem(table.name, item);
      } catch (e) {
        console.error(`Failed to restore item in table ${table.name}:`, e);
      }
    }
  }
}
