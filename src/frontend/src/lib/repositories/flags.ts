import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned, verifyFlagDismissal, verifyOwnFlag, verifyReportedFlag } from '$lib/verifiers';

/** A flag as kept: countersigned, or, for one the viewer raised that the
 * server already held, just the reed and the viewer. An admin's copy
 * carries its case's dismissal once there is one. */
export type StoredFlag = Pick<api.ReedFlag, 'reedId' | 'reporterId'> & Partial<api.ReedFlag>;

type CaseState = 'open' | 'dismissed' | 'void';

/** An admin's index row for a case, derived from its flags: never verified
 * or reported. sortAt is its latest flag, its dismissal, or when it went
 * void, by state. */
interface CaseRow {
  id: string;
  reedId: string;
  authorId: string;
  state: CaseState;
  sortAt: number;
  dismissal?: api.FlagDismissal;
  voidedAt?: number;
}

/** A case for the admin flags list: a reed's flags, newest first, until a
 * dismissal closes them or all are withdrawn. */
export interface FlagCase {
  id: string;
  reedId: string;
  authorId: string;
  flags: api.ReedFlag[];
  dismissal?: api.FlagDismissal;
  voidedAt?: number;
  /** For paging: where the case sorts within its state. */
  sortAt: number;
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

async function verifyStoredFlag(flag: StoredFlag): Promise<boolean> {
  if (!flag.serverSignature) return flag.reporterId === viewerID() ? allowUnsigned(flag) : false;
  const valid = flag.reporterId === viewerID() ? verifyOwnFlag(flag as api.ReedFlag) : verifyReportedFlag(flag as api.ReedFlag);
  if (!(await valid)) return false;
  return !flag.dismissal || (flag.dismissal.caseId === flag.caseId && verifyFlagDismissal(flag.dismissal));
}

function countersigned(flags: StoredFlag[]): api.ReedFlag[] {
  return flags.filter((f): f is api.ReedFlag => !!f.serverSignature);
}

function newestFirst(flags: api.ReedFlag[]): api.ReedFlag[] {
  return flags.sort((a, b) => (b.serverSignature?.signedAt ?? 0) - (a.serverSignature?.signedAt ?? 0));
}

/** Every key starting with `${authorId}/`, a reed of theirs. */
function reedsOf(authorId: string): IDBKeyRange {
  return IDBKeyRange.bound(`${authorId}/`, `${authorId}/￿`);
}

/** Flags, and the cases derived from them. A flag changes only when
 * withdrawn or when its case is dismissed; a dismissal never changes. */
export const flagsRepository = {
  async isWithdrawn(reedId: string, reporterId: string): Promise<boolean> {
    return !!(await dbService.get<StoredFlag>('flaggedReeds', [reedId, reporterId]))?.withdrawal;
  },

  /** The viewer's own flag on reedId, as held. */
  async ownFlag(reedId: string): Promise<StoredFlag | null> {
    return dbService.get<StoredFlag>('flaggedReeds', [reedId, viewerID()]);
  },

  /** Stores flag unless it's held: a withdrawn copy, or one carrying its
   * case's dismissal, replaces one without, never the reverse. */
  async putFlag(flag: StoredFlag): Promise<void> {
    const held = await dbService.get<StoredFlag>('flaggedReeds', [flag.reedId, flag.reporterId]);
    if (held?.serverSignature) {
      const newer = flag.serverSignature && ((flag.withdrawal && !held.withdrawal) || (flag.dismissal && !held.dismissal));
      if (!newer) return;
      flag = { ...flag, withdrawal: flag.withdrawal ?? held.withdrawal, dismissal: flag.dismissal ?? held.dismissal };
    }
    await dbService.put('flaggedReeds', flag, verifyStoredFlag);
  },

  async flagsOf(reedId: string): Promise<api.ReedFlag[]> {
    return countersigned(await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reedId', reedId));
  },

  async flagsInCase(caseId: string): Promise<api.ReedFlag[]> {
    return newestFirst(countersigned(await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'caseId', caseId)));
  },

  async hasDismissal(caseId: string): Promise<boolean> {
    return !!(await dbService.get<CaseRow>('flagCases', caseId))?.dismissal;
  },

  async caseState(caseId: string): Promise<CaseState | null> {
    return (await dbService.get<CaseRow>('flagCases', caseId))?.state ?? null;
  },

  /** Writes d on every flag held of its case, then settles the case. */
  async putDismissal(d: api.FlagDismissal): Promise<void> {
    if (!(await verifyFlagDismissal(d))) throw new Error(`dismissal of ${d.caseId} does not verify`);
    for (const f of await this.flagsInCase(d.caseId)) {
      if (!f.dismissal) await dbService.put('flaggedReeds', { ...f, dismissal: d }, allowUnsigned);
    }
    await this.settleCase(d.caseId);
  },

  /** Brings caseId's index row in line with the flags held of it. */
  async settleCase(caseId: string): Promise<void> {
    const flags = await this.flagsInCase(caseId);
    if (flags.length === 0) {
      await dbService.delete('flagCases', caseId);
      return;
    }
    const dismissal = flags.find((f) => f.dismissal)?.dismissal;
    const withdrawals = flags.map((f) => f.withdrawal?.serverSignature?.signedAt ?? 0);
    const row: CaseRow = { id: caseId, reedId: flags[0].reedId, authorId: authorOf(flags[0].reedId), state: 'open', sortAt: 0 };
    if (dismissal) {
      row.state = 'dismissed';
      row.dismissal = dismissal;
      row.sortAt = dismissal.serverSignature?.signedAt ?? 0;
    } else if (withdrawals.every((at) => at > 0)) {
      row.state = 'void';
      row.voidedAt = row.sortAt = Math.max(...withdrawals);
    } else {
      row.sortAt = flags[0].serverSignature?.signedAt ?? 0;
    }
    await dbService.put<CaseRow>('flagCases', row, allowUnsigned);
  },

  /** Cases in state newest first, after the last one shown. */
  async cases(state: CaseState, limit: number, after?: FlagCase): Promise<FlagCase[]> {
    const start = after ? [state, after.sortAt, after.id] : [state, Number.MAX_SAFE_INTEGER, '￿'];
    const rows = await dbService.getLatestFromIndex<CaseRow>('flagCases', 'byState', limit, (r) => r.state === state, start);
    return Promise.all(rows.map(async (row) => ({ ...row, flags: await this.flagsInCase(row.id) })));
  },

  /** Every flag authorId's reeds received, in cases, newest first. */
  async historyOf(authorId: string): Promise<FlagHistory> {
    const flags = countersigned(await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reedId', reedsOf(authorId)));
    const byCase = new Map<string, api.ReedFlag[]>();
    for (const f of flags) byCase.set(f.caseId, [...(byCase.get(f.caseId) ?? []), f]);
    const cases: FlagCase[] = [...byCase].map(([id, caseFlags]) => {
      newestFirst(caseFlags);
      const dismissal = caseFlags.find((f) => f.dismissal)?.dismissal;
      const sortAt = dismissal?.serverSignature?.signedAt ?? caseFlags[0].serverSignature?.signedAt ?? 0;
      return { id, reedId: caseFlags[0].reedId, authorId, flags: caseFlags, dismissal, sortAt };
    });
    cases.sort((a, b) => b.sortAt - a.sortAt);
    return { count: flags.filter((f) => !f.withdrawal).length, cases };
  },

  /** Drops a case an admin deleted, with its flags. */
  async deleteCase(caseId: string): Promise<void> {
    for (const f of await this.flagsInCase(caseId)) await dbService.delete('flaggedReeds', [f.reedId, f.reporterId]);
    await dbService.delete('flagCases', caseId);
  },

  /** Drops a removed account's flags in open cases, and settles those. */
  async removeReporter(userId: string): Promise<void> {
    const flags = await dbService.getAllByIndex<StoredFlag>('flaggedReeds', 'reporterId', userId);
    const touched = new Set<string>();
    for (const f of flags) {
      if (!f.caseId || f.dismissal) continue;
      const row = await dbService.get<CaseRow>('flagCases', f.caseId);
      if (row && row.state !== 'open') continue;
      await dbService.delete('flaggedReeds', [f.reedId, f.reporterId]);
      touched.add(f.caseId);
    }
    for (const caseId of touched) await this.settleCase(caseId);
  },

  /** Drops what the viewer kept as an admin: everyone else's flags and the
   * cases. */
  async clearAdminCopies(): Promise<void> {
    const me = viewerID();
    for (const f of await dbService.getAll<StoredFlag>('flaggedReeds')) {
      if (f.reporterId !== me) await dbService.delete('flaggedReeds', [f.reedId, f.reporterId]);
    }
    await dbService.clear('flagCases');
  },
};
