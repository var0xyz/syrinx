import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyVouch } from '$lib/verifiers';
import { notifyVouchesChanged } from '$lib/stores/vouchChanges';

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
    notifyVouchesChanged();
  },

  async get(vouchID: string): Promise<VouchRecord | null> {
    return dbService.get<VouchRecord>('vouches', vouchID);
  },

  async has(vouchID: string): Promise<boolean> {
    return !!(await vouchesRepository.get(vouchID));
  },

  /** Your own are kept even once withdrawn: the audit list needs a
   * complete record. Ownership comes from the vouch id's owner prefix. */
  async delete(vouchID: string, voucherUserID: string | null): Promise<void> {
    if (voucherUserID && vouchID.startsWith(`${voucherUserID}/`)) return;
    await dbService.delete('vouches', vouchID);
    notifyVouchesChanged();
  },

  /** Every verified vouch naming this user, withdrawn ones included. */
  async forSubject(subjectUserID: string): Promise<VouchRecord[]> {
    return dbService.getAllByIndex<VouchRecord>('vouches', 'subjectUserID', subjectUserID);
  },

  /** The audit list's source, newest first by countersignature time.
   * Local: the server can omit a row, though never forge one. */
  async byVoucher(voucherUserID: string): Promise<VouchRecord[]> {
    const held = await dbService.getAllByIndex<VouchRecord>(
      'vouches',
      'voucherUserID',
      voucherUserID
    );
    return held.sort((a, b) =>
      b.serverSignature.timestamp.localeCompare(a.serverSignature.timestamp)
    );
  },
};
