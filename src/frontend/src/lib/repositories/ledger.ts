import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyLedgerRecord } from '$lib/verifiers';

/** A ledger entry as stored. IDs are numbers here: IndexedDB keys can't be
 * the wire's bigint. */
export interface LedgerRecord {
  id: number;
  prevID?: number;
  /** What the entry records: request, admin-invite, mesh or reserved-name. */
  kind: string;
  userID: string;
  /** Who proposed the request or admin invite this entry completes, and when. */
  requesterID: string;
  requestedAt?: number;
  message: string;
  content: string;
  targets: string[];
  serverSignature: api.ServerSignature;
}

export function ledgerRecord(entry: api.LedgerEntry): LedgerRecord {
  return {
    id: Number(entry.id),
    prevID: entry.prevId === undefined ? undefined : Number(entry.prevId),
    kind: entry.kind,
    userID: entry.userId,
    requesterID: entry.requesterId,
    requestedAt: entry.requestedAt || undefined,
    message: entry.message,
    content: entry.content,
    targets: [...entry.targets],
    serverSignature: entry.serverSignature!,
  };
}

/** The verified ledger, keyed by entry ID. */
export const ledgerRepository = {
  async put(record: LedgerRecord): Promise<void> {
    await dbService.put('ledger', record, verifyLedgerRecord);
  },

  async get(id: number): Promise<LedgerRecord | null> {
    return dbService.get<LedgerRecord>('ledger', id);
  },

  async getAll(): Promise<LedgerRecord[]> {
    return dbService.getAll<LedgerRecord>('ledger');
  },
};

/** records in chain order, from the head. Entries off the chain are left
 * out. */
export function chainOrder(records: LedgerRecord[]): LedgerRecord[] {
  const next = new Map<number | undefined, LedgerRecord>();
  for (const r of records) next.set(r.prevID, r);
  const out: LedgerRecord[] = [];
  for (let r = next.get(undefined); r; r = next.get(r.id)) out.push(r);
  return out;
}
