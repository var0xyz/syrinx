import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/** A key minted on the lost-device page. The private key is wrapped at
 * rest like every other. */
export interface PendingRebindRecord {
  userID: string;
  keyID: string;
  fingerprint: string;
  publicKey: string;
  privateKey: string;
  createdAt: number;
  /** The key finishing rotates to, kept so a retry rotates to the same one. */
  fresh?: { keyID: string; publicKey: string; privateKey: string };
  /** The fresh key as the server countersigned it, once rotated. */
  rotated?: unknown;
}

export const pendingRebindRepository = {
  async put(record: PendingRebindRecord): Promise<void> {
    await dbService.put('pendingRebind', record, allowUnsigned);
  },

  async getAll(): Promise<PendingRebindRecord[]> {
    return dbService.getAll<PendingRebindRecord>('pendingRebind');
  },

  async delete(userID: string): Promise<void> {
    await dbService.delete('pendingRebind', userID);
  },
};
