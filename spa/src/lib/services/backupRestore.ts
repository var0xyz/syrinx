import type * as api from '$lib/types/api';
import { cryptoService } from './crypto';
import { dbService } from './db';

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

const BACKUP_FILENAME_RE = /^syrinx-.+\.sxb\.gz\.gpg$/i;

export function isBackupFilename(name: string): boolean {
  return BACKUP_FILENAME_RE.test(name);
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

  if (!profile?.id || !profile.username || !profile.signatureFingerprint || !profile.server) {
    throw new Error('Invalid backup file: missing countersigned profile.');
  }

  return profile;
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
    (k) => k && typeof k === 'object' && (k as { fingerprint?: string }).fingerprint === keyFingerprint
  ) as { armor?: string } | undefined;

  if (!userId || !keyFingerprint || !keyPassphrase || !privateKeyEntry?.armor) {
    throw new Error('Invalid backup file: missing required identity data.');
  }
}

/**
 * Write backup contents to localStorage and IndexedDB.
 */
export async function writeBackup(backup: BackupPayload): Promise<void> {
  const ls = backup.localStorage ?? {};
  for (const [key, value] of Object.entries(ls)) {
    localStorage.setItem(key, value);
  }

  await dbService.init();
  for (const table of backup.indexedDB?.tables ?? []) {
    for (const item of table.items ?? []) {
      try {
        await dbService.put(table.name, item as api.Base);
      } catch (e) {
        console.error(`Failed to restore item in table ${table.name}:`, e);
      }
    }
  }
}
