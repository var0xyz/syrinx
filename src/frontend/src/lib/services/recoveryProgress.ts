/**
 * Recovery progress ledger: enumerate restored IndexedDB work units, persist
 * per-entity / page timestamps, and expose a generic percent summary.
 * Network execution of units is separate.
 */

import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';
import { dbService } from './db';
import { canAssembleKeyNest } from './recoveryKeyNest';
import { enumerateRecoveryRecords, type RecoveryRecordRef } from './recoveryRecords';

export const RECOVERY_PROGRESS_KEY = 'recoveryProgress';

export const OWN_IDENTITY_KEY = 'own_identity';
export const SERVERS_KEY = 'servers';
export const RECORDS_KEY = 'records';
export const PINS_KEY = 'pins';
export const RECORD_PAGE_SIZE = 100;
export const COMPLETE_KEY = 'complete';

export type RecoveryEntityEntry = {
  startTime?: number;
  endTime?: number;
  skipped?: boolean;
  skipReason?: string;
};

export type RecoveryRecordPage = {
  index: number;
  refs: RecoveryRecordRef[];
  startTime?: number;
  endTime?: number;
};

export type RecoveryRecordsEntry = {
  pages: RecoveryRecordPage[];
};

export type RecoveryProgressEntity = RecoveryEntityEntry | RecoveryRecordsEntry;

export type RecoveryProgressLedger = {
  version: 1;
  entities: Record<string, RecoveryProgressEntity>;
};

export type RecoveryProgressSummary = {
  completed: number;
  total: number;
  percent: number;
  elapsedMs: number;
};

export type EnumerateInput = {
  selfUserId: string;
  users: api.User[];
  usersInfo: api.UserInfo[];
  publicKeys: api.PublicKey[];
  revocations: api.KeyRevocation[];
  reeds: ReedType[];
  records: RecoveryRecordRef[];
  /** How many peer records the device holds. */
  servers: number;
};

function readJson<T>(key: string): T | null {
  if (typeof localStorage === 'undefined') return null;
  const raw = localStorage.getItem(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function writeLedger(ledger: RecoveryProgressLedger): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.setItem(RECOVERY_PROGRESS_KEY, JSON.stringify(ledger));
}

export function peerEntityKey(userId: string): string {
  return `peer:${userId}`;
}

export function reedEntityKey(reedId: string): string {
  return `reed:${reedId}`;
}

/** Records are paged; everything else is one unit. */
function isPagedEntry(
  entry: RecoveryProgressEntity | undefined
): entry is RecoveryRecordsEntry {
  return !!entry && Array.isArray((entry as RecoveryRecordsEntry).pages);
}

export function isRecordsEntry(
  entry: RecoveryProgressEntity | undefined
): entry is RecoveryRecordsEntry {
  return isPagedEntry(entry) && entry.pages.every((page) => 'refs' in page);
}

export function isEntityEntry(
  entry: RecoveryProgressEntity | undefined
): entry is RecoveryEntityEntry {
  return !!entry && !isPagedEntry(entry);
}

export function getRecoveryProgress(): RecoveryProgressLedger | null {
  const ledger = readJson<RecoveryProgressLedger>(RECOVERY_PROGRESS_KEY);
  if (!ledger || ledger.version !== 1 || typeof ledger.entities !== 'object') {
    return null;
  }
  return ledger;
}

export function hasRecoveryProgress(): boolean {
  return getRecoveryProgress() != null;
}

/** Empty ledger shell (start of a fresh recovery run). */
export function initEmptyRecoveryProgress(): void {
  writeLedger({ version: 1, entities: {} });
}

export function clearRecoveryProgress(): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem(RECOVERY_PROGRESS_KEY);
}

function chunk<T>(items: T[], size: number): T[][] {
  const pages: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    pages.push(items.slice(i, i + size));
  }
  return pages;
}

/**
 * Build the desired work set from restored stores (pure).
 * Skipped peers are included as completed-for-progress entries. Users and
 * reeds of other servers are left out: this server can't restore them.
 */
export function enumerateRecoveryWork(input: EnumerateInput): RecoveryProgressLedger {
  const entities: Record<string, RecoveryProgressEntity> = {};

  entities[OWN_IDENTITY_KEY] = {};
  if (input.servers > 0) entities[SERVERS_KEY] = {};

  const byFp = new Map<string, api.PublicKey>();
  for (const key of input.publicKeys) {
    byFp.set(key.id.toLowerCase(), key);
  }
  const byRevocationId = new Map<string, api.KeyRevocation>();
  for (const revocation of input.revocations) {
    byRevocationId.set(revocation.id.toLowerCase(), revocation);
  }
  const byUser = new Map<string, api.User>();
  for (const user of input.users) {
    byUser.set(user.id, user);
  }
  const byInfo = new Map<string, api.UserInfo>();
  for (const info of input.usersInfo) {
    byInfo.set(info.id, info);
  }

  const lookups = {
    getUser: (id: string) => byUser.get(id),
    getActiveKeyId: (id: string) =>
      byInfo.get(id)?.activeKeyId ||
      (byUser.get(id) as api.User & { activeKeyId?: string } | undefined)
        ?.activeKeyId,
    getPublicKey: (fp: string) => byFp.get(fp.toLowerCase()) ?? byFp.get(fp),
    getRevocation: (id: string) => byRevocationId.get(id.toLowerCase()) ?? byRevocationId.get(id),
  };

  for (const user of input.users) {
    if (user.id === input.selfUserId) continue;
    const nest = canAssembleKeyNest(user.id, lookups);
    if (nest.ok === true) {
      entities[peerEntityKey(user.id)] = {};
    } else {
      entities[peerEntityKey(user.id)] = {
        skipped: true,
        skipReason: nest.reason,
        endTime: Date.now(),
      };
    }
  }

  for (const reed of input.reeds) {
    if (!reed.serverSignature || !reed.id) continue;
    entities[reedEntityKey(reed.id)] = {};
  }

  entities[RECORDS_KEY] = {
    pages: chunk(input.records, RECORD_PAGE_SIZE).map((refs, index) => ({ index, refs })),
  };

  const ownPins = byInfo.get(input.selfUserId)?.pinnedReedIds;
  if (ownPins?.length) entities[PINS_KEY] = {};

  entities[COMPLETE_KEY] = {};

  return { version: 1, entities };
}

function mergeEntity(
  desired: RecoveryEntityEntry,
  existing: RecoveryEntityEntry | undefined
): RecoveryEntityEntry {
  if (!existing) return { ...desired };
  // Preserve progress timestamps / skip state from a mid-run ledger.
  return {
    ...desired,
    startTime: existing.startTime ?? desired.startTime,
    endTime: existing.endTime ?? desired.endTime,
    skipped: existing.skipped ?? desired.skipped,
    skipReason: existing.skipReason ?? desired.skipReason,
  };
}

/** A finished records page keeps what it sent; others take the fresh refs. */
function mergeRecords(
  desired: RecoveryRecordsEntry,
  existing: RecoveryRecordsEntry | undefined
): RecoveryRecordsEntry {
  const byIndex = new Map(existing?.pages.map((page) => [page.index, page]));
  return {
    pages: desired.pages.map((page) => {
      const prev = byIndex.get(page.index);
      return prev?.endTime != null ? { ...prev } : { ...page, startTime: prev?.startTime };
    }),
  };
}

/** Merge a freshly enumerated ledger with any in-progress timestamps. */
export function mergeRecoveryProgress(
  desired: RecoveryProgressLedger,
  existing: RecoveryProgressLedger | null
): RecoveryProgressLedger {
  if (!existing) return desired;
  const entities: Record<string, RecoveryProgressEntity> = {};
  for (const [key, desiredEntry] of Object.entries(desired.entities)) {
    const prev = existing.entities[key];
    if (key === RECORDS_KEY && isRecordsEntry(desiredEntry)) {
      entities[key] = mergeRecords(desiredEntry, isRecordsEntry(prev) ? prev : undefined);
    } else if (isEntityEntry(desiredEntry)) {
      entities[key] = mergeEntity(
        desiredEntry,
        isEntityEntry(prev) ? prev : undefined
      );
    } else {
      entities[key] = desiredEntry;
    }
  }
  return { version: 1, entities };
}

function isUnitDone(entry: RecoveryEntityEntry): boolean {
  return entry.endTime != null || entry.skipped === true;
}

/**
 * completed / total units. Skipped peers count as completed.
 * Each follow or records page is one unit; an empty list adds 0 units.
 */
export function summarizeRecoveryProgress(
  ledger: RecoveryProgressLedger
): RecoveryProgressSummary {
  let completed = 0;
  let total = 0;
  let elapsedMs = 0;

  for (const [key, entry] of Object.entries(ledger.entities)) {
    if (isPagedEntry(entry)) {
      for (const page of entry.pages) {
        total += 1;
        if (page.endTime != null) {
          completed += 1;
          if (page.startTime != null) {
            elapsedMs += Math.max(0, page.endTime - page.startTime);
          }
        }
      }
      continue;
    }
    if (!isEntityEntry(entry)) continue;
    total += 1;
    if (isUnitDone(entry)) {
      completed += 1;
      if (entry.startTime != null && entry.endTime != null && !entry.skipped) {
        elapsedMs += Math.max(0, entry.endTime - entry.startTime);
      }
    }
  }

  const percent = total === 0 ? 100 : Math.round((completed / total) * 100);
  return { completed, total, percent, elapsedMs };
}

export function getRecoveryProgressSummary(): RecoveryProgressSummary {
  const ledger = getRecoveryProgress();
  if (!ledger) {
    return { completed: 0, total: 0, percent: 0, elapsedMs: 0 };
  }
  return summarizeRecoveryProgress(ledger);
}

/**
 * Load restored IndexedDB, enumerate work, merge with any existing ledger,
 * and persist. Safe to call on resume / refresh.
 */
export async function ensureRecoveryProgress(): Promise<RecoveryProgressLedger> {
  const selfUserId =
    typeof localStorage !== 'undefined' ? localStorage.getItem('userId') || '' : '';

  const [users, usersInfo, publicKeys, revocations, reeds, records, servers] = await Promise.all([
    dbService.getAll<api.User>('users'),
    dbService.getAll<api.UserInfo>('usersInfo'),
    dbService.getAll<api.PublicKey>('publicKeys'),
    dbService.getAll<api.KeyRevocation>('revocations'),
    dbService.getAll<ReedType>('reeds'),
    enumerateRecoveryRecords(),
    dbService.getAll('federatedServers'),
  ]);

  const desired = enumerateRecoveryWork({
    selfUserId,
    users,
    usersInfo,
    publicKeys,
    revocations,
    reeds,
    records,
    servers: servers.length,
  });
  const merged = mergeRecoveryProgress(desired, getRecoveryProgress());
  writeLedger(merged);
  return merged;
}

function updateEntity(
  key: string,
  patch: (entry: RecoveryEntityEntry) => RecoveryEntityEntry
): void {
  const ledger = getRecoveryProgress() ?? { version: 1 as const, entities: {} };
  const current = ledger.entities[key];
  if (isPagedEntry(current)) {
    throw new Error(`recoveryProgress: ${key} is a paged group, not an entity`);
  }
  ledger.entities[key] = patch(isEntityEntry(current) ? { ...current } : {});
  writeLedger(ledger);
}

export function markEntityStarted(key: string, at: number = Date.now()): void {
  updateEntity(key, (entry) => ({
    ...entry,
    startTime: entry.startTime ?? at,
    skipped: undefined,
    skipReason: undefined,
  }));
}

export function markEntityFinished(key: string, at: number = Date.now()): void {
  updateEntity(key, (entry) => ({
    ...entry,
    startTime: entry.startTime ?? at,
    endTime: at,
    skipped: undefined,
    skipReason: undefined,
  }));
}

export function markEntitySkipped(
  key: string,
  reason: string,
  at: number = Date.now()
): void {
  updateEntity(key, (entry) => ({
    ...entry,
    skipped: true,
    skipReason: reason,
    endTime: at,
    startTime: entry.startTime,
  }));
}

function updateRecordPage(pageIndex: number, patch: (page: RecoveryRecordPage) => void): void {
  const ledger = getRecoveryProgress() ?? { version: 1 as const, entities: {} };
  const records = ledger.entities[RECORDS_KEY];
  const page = isRecordsEntry(records) ? records.pages.find((p) => p.index === pageIndex) : undefined;
  if (!page) {
    throw new Error(`recoveryProgress: records page ${pageIndex} missing`);
  }
  patch(page);
  writeLedger(ledger);
}

export function markRecordPageStarted(pageIndex: number, at: number = Date.now()): void {
  updateRecordPage(pageIndex, (page) => {
    page.startTime ??= at;
  });
}

export function markRecordPageFinished(pageIndex: number, at: number = Date.now()): void {
  updateRecordPage(pageIndex, (page) => {
    page.startTime ??= at;
    page.endTime = at;
  });
}
