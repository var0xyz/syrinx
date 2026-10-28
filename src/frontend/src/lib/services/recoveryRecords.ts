/** The signed records a device reports back to a rebuilt server, one local
 * store per record kind. A new kind is one entry here and one on the server. */

import type { MessageInitShape } from '@bufbuild/protobuf';
import type { RecoveryRecordSchema } from '$lib/proto/recovery_pb';
import type * as api from '$lib/types/api';
import { chainOrder, type LedgerRecord } from '$lib/repositories/ledger';
import type { LikedReedRecord } from '$lib/repositories/likedReeds';
import type { StoredFlag } from '$lib/repositories/flags';
import { dbService, type DbKey } from './db';

type RecoveryRecord = MessageInitShape<typeof RecoveryRecordSchema>;

/** Where one reported record lives on this device. */
export type RecoveryRecordRef = { store: string; key: DbKey };

type RecordSource<T> = {
  store: string;
  key: (item: T) => DbKey;
  /** The records in the order they're best reported in. */
  order?: (items: T[]) => T[];
  record: (item: T) => RecoveryRecord['record'] | null;
};

function source<T>(s: RecordSource<T>): RecordSource<unknown> {
  return s as RecordSource<unknown>;
}

// Each record before what refers to it: the ledger in chain order, a
// thread before its removal.
const SOURCES: RecordSource<unknown>[] = [
  source<LedgerRecord>({
    store: 'ledger',
    key: (r) => r.id,
    order: chainOrder,
    record: (r) => ({
      case: 'ledgerEntry',
      value: {
        id: BigInt(r.id),
        prevId: r.prevID === undefined ? undefined : BigInt(r.prevID),
        kind: r.kind,
        userId: r.userID,
        requesterId: r.requesterID,
        requestedAt: r.requestedAt,
        message: r.message,
        content: r.content,
        targets: r.targets,
        serverSignature: r.serverSignature,
      },
    }),
  }),
  source<api.AdminRequest>({ store: 'adminRequests', key: (r) => r.id, record: (r) => ({ case: 'adminRequest', value: r }) }),
  source<api.ThreadRecord>({ store: 'threads', key: (r) => r.threadId, record: (r) => ({ case: 'thread', value: r }) }),
  source<api.ThreadRemoval>({
    store: 'removedThreads',
    key: (r) => r.cert!.threadId,
    record: (r) => ({ case: 'threadRemoval', value: r }),
  }),
  source<api.ReedRemoval>({ store: 'removedReeds', key: (r) => r.reedId, record: (r) => ({ case: 'reedRemoval', value: r }) }),
  source<api.AccountRemoval>({
    store: 'removedAccounts',
    key: (r) => r.userId,
    record: (r) => ({ case: 'accountRemoval', value: r }),
  }),
  source<api.Vouch>({ store: 'vouches', key: (r) => r.id, record: (r) => ({ case: 'vouch', value: r }) }),
  source<LikedReedRecord>({
    store: 'likedReeds',
    key: (r) => r.compositeKey,
    record: ({ compositeKey: _key, likedAt: _at, ...like }) => ({ case: 'like', value: like }),
  }),
  source<api.BlockCert>({ store: 'blocks', key: (r) => r.blockedUserId, record: (r) => ({ case: 'block', value: r }) }),
  // Blocks against this device's user, a peer's users' among them.
  source<api.BlockCert>({ store: 'blockedBy', key: (r) => r.userId, record: (r) => ({ case: 'block', value: r }) }),
  // Follows from both ends: the server keeps the newest per pair.
  source<api.FollowCert>({ store: 'followCerts', key: (r) => r.followeeId, record: (r) => ({ case: 'follow', value: r }) }),
  source<api.FollowCert>({ store: 'followers', key: (r) => r.followerId, record: (r) => ({ case: 'follow', value: r }) }),
  // The viewer's own flags, and an admin's copy of everyone's, which
  // carries its case's dismissal.
  source<StoredFlag>({
    store: 'flaggedReeds',
    key: (r) => [r.reedId, r.reporterId],
    record: (r) => (r.serverSignature ? { case: 'flag', value: r as api.ReedFlag } : null),
  }),
];

/** Every record this device holds, as refs, in reporting order. */
export async function enumerateRecoveryRecords(): Promise<RecoveryRecordRef[]> {
  const refs: RecoveryRecordRef[] = [];
  for (const s of SOURCES) {
    const items = await dbService.getAll<unknown>(s.store);
    for (const item of s.order ? s.order(items) : items) {
      refs.push({ store: s.store, key: s.key(item) });
    }
  }
  return refs;
}

/** The records refs name, skipping any no longer held. */
export async function loadRecoveryRecords(refs: RecoveryRecordRef[]): Promise<RecoveryRecord[]> {
  const out: RecoveryRecord[] = [];
  for (const ref of refs) {
    const s = SOURCES.find((candidate) => candidate.store === ref.store);
    const item = s && (await dbService.get<api.Base>(ref.store, ref.key));
    const record = item && s.record(item);
    if (record) out.push({ record });
  }
  return out;
}
