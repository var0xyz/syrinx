import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyVouch } from '$lib/verifiers';

/**
 * A vouch this client verified itself. Nothing reaches this store without
 * passing verifyVouch, so its mere presence is the evidence a mark needs.
 */
export interface VouchRecord extends api.Vouch {
  /** When this client verified it, for debugging a stale local set. */
  verifiedAt: string;
}

export const vouchesRepository = {
  /** Verification runs inside dbService.put and throws on failure. */
  async put(cert: api.Vouch): Promise<void> {
    const record: VouchRecord = { ...cert, verifiedAt: new Date().toISOString() };
    await dbService.put('vouches', record, verifyVouch);
  },

  async get(vouchID: string): Promise<VouchRecord | null> {
    return dbService.get<VouchRecord>('vouches', vouchID);
  },

  async has(vouchID: string): Promise<boolean> {
    return !!(await vouchesRepository.get(vouchID));
  },

  async delete(vouchID: string): Promise<void> {
    await dbService.delete('vouches', vouchID);
  },

  /** Every verified vouch naming this user, withdrawn ones included. */
  async forSubject(subjectUserID: string): Promise<VouchRecord[]> {
    return dbService.getAllByIndex<VouchRecord>('vouches', 'subjectUserID', subjectUserID);
  },

  /** Vouches this user made, which is what the audit list reads. */
  async byVoucher(voucherUserID: string): Promise<VouchRecord[]> {
    return dbService.getAllByIndex<VouchRecord>('vouches', 'voucherUserID', voucherUserID);
  },
};
