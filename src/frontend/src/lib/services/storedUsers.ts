import { dbService } from './db';
import { protectedUserIDs } from './eviction';
import { profileVisitsRepository } from '$lib/repositories/profileVisits';
import { pendingEvictionsRepository } from '$lib/repositories/pendingEvictions';
import { pendingKeyEvictionsRepository } from '$lib/repositories/pendingKeyEvictions';
import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';

/** What this device holds of one other user: content they authored only. */
export interface StoredUser {
  userID: string;
  /** Null for a removed account, or a user whose profile we don't hold. */
  username: string | null;
  reeds: number;
  bytes: number;
  /** Their profile and keys' share of `bytes`, kept when `locked`. */
  keptBytes: number;
  locked: boolean;
  removed: boolean;
  /** They blocked the viewer: only their keys and the block are held. */
  blockedYou: boolean;
  evicting: boolean;
  lastVisitedAt: number | null;
}

/** Walks the stores on demand, so there is no running total to drift. */
export async function listStoredUsers(): Promise<StoredUser[]> {
  const viewerID = localStorage.getItem('userId');
  const [reeds, profiles, keys, removed, visits, queuedReeds, queuedKeys, locked, blocks] = await Promise.all([
    dbService.getAllWithMeta<ReedType>('reeds'),
    dbService.getAllWithMeta<api.User>('users'),
    dbService.getAllWithMeta<api.PublicKey>('publicKeys'),
    dbService.getAll<api.AccountRemoval>('removedAccounts'),
    profileVisitsRepository.getAll(),
    pendingEvictionsRepository.getAll(),
    pendingKeyEvictionsRepository.getAll(),
    protectedUserIDs(),
    dbService.getAllWithMeta<api.BlockCert>('blockedBy'),
  ]);

  const byUser = new Map<string, StoredUser>();
  const rowFor = (userID: string): StoredUser => {
    let row = byUser.get(userID);
    if (!row) {
      row = {
        userID,
        username: null,
        reeds: 0,
        bytes: 0,
        keptBytes: 0,
        locked: locked.has(userID),
        removed: false,
        blockedYou: false,
        evicting: false,
        lastVisitedAt: null,
      };
      byUser.set(userID, row);
    }
    return row;
  };

  for (const { record, meta } of reeds) {
    if (!record.userID) continue;
    const row = rowFor(record.userID);
    row.reeds++;
    row.bytes += meta?.bytes ?? 0;
  }
  for (const { record, meta } of profiles) {
    if (!record.id) continue;
    const row = rowFor(record.id);
    row.username = record.username || null;
    row.bytes += meta?.bytes ?? 0;
    row.keptBytes += meta?.bytes ?? 0;
  }
  for (const { record, meta } of keys) {
    if (!record.userId) continue;
    const row = rowFor(record.userId);
    row.bytes += meta?.bytes ?? 0;
    row.keptBytes += meta?.bytes ?? 0;
  }

  for (const { record, meta } of blocks) {
    if (!record.userId) continue;
    const row = rowFor(record.userId);
    row.blockedYou = true;
    row.bytes += meta?.bytes ?? 0;
  }

  // Only annotate users we hold something of; these never add a row.
  for (const cert of removed) {
    const row = byUser.get(cert.userId);
    if (row) row.removed = true;
  }
  for (const visit of visits) {
    const row = byUser.get(visit.userID);
    if (row) row.lastVisitedAt = visit.visitedAt;
  }
  for (const { userID } of [...queuedReeds, ...queuedKeys]) {
    const row = byUser.get(userID);
    if (row) row.evicting = true;
  }

  if (viewerID) byUser.delete(viewerID);
  return [...byUser.values()];
}

/** What evicting `row` would free: a locked user keeps profile and keys. */
export function freeableBytes(row: StoredUser): number {
  return row.locked ? row.bytes - row.keptBytes : row.bytes;
}
