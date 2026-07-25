import type * as api from '$lib/types/api';
import { cryptoService } from './crypto';
import { dbService } from './db';
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

  if (
    !profile?.id ||
    !profile.username ||
    !profile.userSignature?.fingerprint ||
    !profile.serverSignature
  ) {
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

async function restoreItem(storeName: string, item: unknown): Promise<void> {
  switch (storeName) {
    case 'publicKeys':
      await publicKeyRepository.put(item as api.PublicKey);
      return;
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
    case 'privateKeys': {
      const key = item as PrivateKey;
      await privateKeyRepository.put(key.fingerprint, key.armor);
      if (key.revoked) {
        await privateKeyRepository.setRevoked(key.fingerprint);
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
  const ls = backup.localStorage ?? {};
  for (const [key, value] of Object.entries(ls)) {
    localStorage.setItem(key, value);
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
