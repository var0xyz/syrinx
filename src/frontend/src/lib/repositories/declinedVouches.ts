import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyVouch } from '$lib/verifiers';

/**
 * Vouches the server holds under the caller's name that the caller said
 * they did not make. The server can't delete them, so they stay listed.
 */
export const declinedVouchesRepository = {
  async put(cert: api.Vouch): Promise<void> {
    await dbService.put('declinedVouches', cert, (c) => verifyVouch(c, c.subjectUserID));
  },

  async delete(vouchID: string): Promise<void> {
    await dbService.delete('declinedVouches', vouchID);
  },

  /** Newest first by countersignature time. */
  async byVoucher(voucherUserID: string): Promise<api.Vouch[]> {
    const held = await dbService.getAllByIndex<api.Vouch>(
      'declinedVouches',
      'voucherUserID',
      voucherUserID
    );
    return held.sort((a, b) =>
      b.serverSignature.timestamp.localeCompare(a.serverSignature.timestamp)
    );
  },
};
