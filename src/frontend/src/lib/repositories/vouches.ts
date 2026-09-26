import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyVouch } from '$lib/verifiers';

/**
 * A vouch this client verified itself. Nothing reaches this store without
 * passing verifyVouch, so its mere presence is the evidence a mark needs.
 */
export type VouchRecord = api.Vouch;

export const vouchesRepository = {
  /**
   * Verification runs inside dbService.put and throws on failure.
   * `subjectUserID` is the user this vouch is being read for — the caller's
   * own context, which is what the signed key id gets checked against.
   */
  async put(cert: api.Vouch, subjectUserID: string): Promise<void> {
    await dbService.put('vouches', cert, (c) => verifyVouch(c, subjectUserID));
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
