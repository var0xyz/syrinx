import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned, verifyFlagDismissal, verifyOwnFlag, verifyReportedFlag } from '$lib/verifiers';

/** A flag as kept: countersigned, or, for one the viewer raised that the
 * server already held, just the reed and the viewer. */
export type StoredFlag = Pick<api.ReedFlag, 'reedId' | 'reporterId'> & Partial<api.ReedFlag>;

/** An admin's index row: a reed with open flags, by its latest one. */
export interface FlagOpenCase {
  reedId: string;
  latestAt: number;
}

/** A page of cases for the admin flags list: a reed's open flags, or one
 * dismissal and the flags it closed. In an author's history, a case
 * without a dismissal also holds the reed's withdrawn flags. */
export interface FlagCase {
  reedId: string;
  authorId: string;
  flags: api.ReedFlag[];
  dismissal?: api.FlagDismissal;
}

/** Every flag an author's reeds received that this device holds. */
export interface FlagHistory {
  /** Open and dismissed flags; withdrawn ones are left out. */
  count: number;
  /** Newest first. */
  cases: FlagCase[];
}

function viewerID(): string {
  return localStorage.getItem('userId') ?? '';
}

function authorOf(reedId: string): string {
  return reedId.slice(0, reedId.lastIndexOf('/'));
}

function verifyStoredFlag(flag: StoredFlag): Promise<boolean> {
  if (!flag.serverSignature) return flag.reporterId === viewerID() ? allowUnsigned(flag) : Promise.resolve(false);
  return flag.reporterId === viewerID() ? verifyOwnFlag(flag as api.ReedFlag) : verifyReportedFlag(flag as api.ReedFlag);
}

function countersigned(flags: StoredFlag[]): api.ReedFlag[] {
  return flags.filter((f): f is api.ReedFlag => !!f.serverSignature);
}

/** Every key starting with `${authorId}/`, a reed of theirs. */
function reedsOf(authorId: string): IDBKeyRange {
  return IDBKeyRange.bound(`${authorId}/`, `${authorId}/\uffff`);
}

function latestAt(c: FlagCase): number {
  if (c.dismissal) return c.dismissal.serverSignature?.signedAt ?? 0;
  return Math.max(0, ...c.flags.map((f) => f.withdrawal?.serverSignature?.signedAt ?? f.serverSignature?.signedAt ?? 0));
}

/** Flags, dismissals, and the open cases derived from them. A flag changes
 * only once, when withdrawn; dismissals never do; the cases follow them. */
export const flagsRepository = {
  async isWithdrawn(reedId: string, reporterId: string): Promise<boolean> {
    return !!(await dbService.get<StoredFlag>('flaggedReeds', [reedId, reporterId]))?.withdrawal;
  },

  /** The viewer's own flag on reedId, as held. */
  async ownFlag(reedId: string): Promise<StoredFlag | null> {
    return dbService.get<StoredFlag>('flaggedReeds', [reedId, viewerID()]);
  },

  /** Stores flag unless it's held: a withdrawn copy replaces an unwithdrawn
   * one, never the reverse. */
  async putFlag(flag: StoredFlag): Promise<void> {
    const held = await dbService.get<StoredFlag>('flaggedReeds', [flag.reedId, flag.reporterId]);
    if (held?.serverSignature && (!flag.serverSignature || held.withdrawal || !flag.withdrawal)) return;
    await dbService.put('flaggedReeds', flag, verifyStoredFlag);
  },

  async flagsOf(reedId: string): Promise<api.ReedFlag[]> {
    return countersigned(await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reedId', reedId));
  },

  async putDismissal(d: api.FlagDismissal): Promise<void> {
    if (await dbService.getMeta('flagDismissals', d.id)) return;
    await dbService.put('flagDismissals', d, verifyFlagDismissal);
  },

  async hasDismissal(id: string): Promise<boolean> {
    return (await dbService.getMeta('flagDismissals', id)) !== null;
  },

  async dismissalsOf(reedId: string): Promise<api.FlagDismissal[]> {
    return dbService.getAllByIndex<api.FlagDismissal>('flagDismissals', 'reedId', reedId);
  },

  /** reedId's flags no dismissal closed and nobody withdrew. */
  async openFlagsOf(reedId: string): Promise<api.ReedFlag[]> {
    const closed = new Set((await this.dismissalsOf(reedId)).flatMap((d) => d.reporters));
    return (await this.flagsOf(reedId)).filter((f) => !closed.has(f.reporterId) && !f.withdrawal);
  },

  /** Every flag authorId's reeds received, in cases, newest first. */
  async historyOf(authorId: string): Promise<FlagHistory> {
    const [flags, dismissals] = await Promise.all([
      dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reedId', reedsOf(authorId)).then(countersigned),
      dbService.getAllByIndex<api.FlagDismissal>('flagDismissals', 'reedId', reedsOf(authorId)),
    ]);
    const cases: FlagCase[] = [];
    const named = new Set<string>();
    for (const d of dismissals) {
      const reporters = new Set(d.reporters);
      const closed = flags.filter((f) => f.reedId === d.reedId && reporters.has(f.reporterId));
      for (const f of closed) named.add(`${f.reedId} ${f.reporterId}`);
      if (closed.length > 0) cases.push({ reedId: d.reedId, authorId, flags: closed, dismissal: d });
    }
    const rest = new Map<string, api.ReedFlag[]>();
    for (const f of flags) {
      if (!named.has(`${f.reedId} ${f.reporterId}`)) rest.set(f.reedId, [...(rest.get(f.reedId) ?? []), f]);
    }
    for (const [reedId, reedFlags] of rest) cases.push({ reedId, authorId, flags: reedFlags });
    cases.sort((a, b) => latestAt(b) - latestAt(a));
    return { count: flags.filter((f) => !f.withdrawal).length, cases };
  },

  /** Brings reedId's open case in line with its flags and dismissals. */
  async settleCase(reedId: string): Promise<void> {
    const open = await this.openFlagsOf(reedId);
    if (open.length === 0) {
      await dbService.delete('flagOpenCases', reedId);
      return;
    }
    const latestAt = Math.max(...open.map((f) => f.serverSignature?.signedAt ?? 0));
    await dbService.put<FlagOpenCase>('flagOpenCases', { reedId, latestAt }, allowUnsigned);
  },

  /** Open cases newest first, after the last one shown. */
  async openCases(limit: number, after?: FlagOpenCase): Promise<FlagCase[]> {
    const rows = await dbService.getLatestFromIndex<FlagOpenCase>(
      'flagOpenCases',
      'byTime',
      limit,
      undefined,
      after ? [after.latestAt, after.reedId] : undefined
    );
    return Promise.all(
      rows.map(async (row) => ({ reedId: row.reedId, authorId: authorOf(row.reedId), flags: await this.openFlagsOf(row.reedId) }))
    );
  },

  /** Dismissals newest first, after the last one shown, each with the flags
   * it closed that are still held. */
  async dismissedCases(limit: number, after?: api.FlagDismissal): Promise<FlagCase[]> {
    const rows = await dbService.getLatestFromIndex<api.FlagDismissal>(
      'flagDismissals',
      'byTime',
      limit,
      undefined,
      after ? [after.serverSignature?.signedAt ?? 0, after.id] : undefined
    );
    return Promise.all(
      rows.map(async (d) => {
        const reporters = new Set(d.reporters);
        const flags = (await this.flagsOf(d.reedId)).filter((f) => reporters.has(f.reporterId));
        return { reedId: d.reedId, authorId: authorOf(d.reedId), flags, dismissal: d };
      })
    );
  },

  /** Withdrawn flags newest withdrawal first, one per case, after the last
   * one shown. */
  async withdrawnCases(limit: number, after?: api.ReedFlag): Promise<FlagCase[]> {
    const rows = await dbService.getLatestFromIndex<api.ReedFlag>(
      'flaggedReeds',
      'byWithdrawal',
      limit,
      undefined,
      after ? [after.withdrawal?.serverSignature?.signedAt ?? 0, after.reedId, after.reporterId] : undefined
    );
    return rows.map((f) => ({ reedId: f.reedId, authorId: authorOf(f.reedId), flags: [f] }));
  },

  /** Drops a removed account's flags, and the dismissals left with none. */
  async removeReporter(userId: string): Promise<void> {
    const flags = await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reporterId', userId);
    for (const f of flags) await dbService.delete('flaggedReeds', [f.reedId, f.reporterId]);
    for (const reedId of new Set(flags.map((f) => f.reedId))) {
      const held = new Set((await this.flagsOf(reedId)).map((f) => f.reporterId));
      for (const d of await this.dismissalsOf(reedId)) {
        if (!d.reporters.some((r) => held.has(r))) await dbService.delete('flagDismissals', d.id);
      }
      await this.settleCase(reedId);
    }
  },

  /** Drops what the viewer kept as an admin: everyone else's flags, the
   * dismissals and the cases. */
  async clearAdminCopies(): Promise<void> {
    const me = viewerID();
    for (const f of await dbService.getAll<StoredFlag>('flaggedReeds')) {
      if (f.reporterId !== me) await dbService.delete('flaggedReeds', [f.reedId, f.reporterId]);
    }
    await dbService.clear('flagDismissals');
    await dbService.clear('flagOpenCases');
  },
};
