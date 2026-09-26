import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { vouchesRepository } from '$lib/repositories/vouches';
import { trustRootsRepository } from '$lib/repositories/trustRoots';
import { allowUnsigned } from '$lib/verifiers';

/**
 * A signed vouch waiting for a connection. Verification happens in a room,
 * often without signal, so the signature must outlive the moment it was
 * made rather than the user having to meet the person again.
 */
export interface PendingVouchRecord {
  compositeKey: string; // subjectKeyID — one pending vouch per key
  subjectUserID: string;
  subjectKeyID: string;
  voucherKeyID: string;
  note: string;
  signature: string; // base64 user detached sig
}

/** Incremented after each successful flush so UI can refresh. */
export const pendingVouchSynced = writable(0);

export const pendingVouchesRepository = {
  async put(record: PendingVouchRecord): Promise<void> {
    await dbService.put('pendingVouches', record, allowUnsigned);
  },

  async delete(subjectKeyID: string): Promise<void> {
    await dbService.delete('pendingVouches', subjectKeyID);
  },

  async get(subjectKeyID: string): Promise<PendingVouchRecord | null> {
    return dbService.get<PendingVouchRecord>('pendingVouches', subjectKeyID);
  },

  async getAll(): Promise<PendingVouchRecord[]> {
    return dbService.getAll<PendingVouchRecord>('pendingVouches');
  },

  /** Idempotent POST flush; the server returns the stored cert on replay. */
  async syncPending(): Promise<void> {
    for (const record of await pendingVouchesRepository.getAll()) {
      try {
        const cert = await apiService.createVouch(
          record.subjectKeyID,
          record.voucherKeyID,
          record.signature,
          record.note
        );
        // put verifies; a cert that fails is not stored and stays queued.
        await vouchesRepository.put(cert, record.subjectUserID);
        await trustRootsRepository.add(record.subjectUserID, record.subjectKeyID);
        await pendingVouchesRepository.delete(record.subjectKeyID);
        pendingVouchSynced.update((n) => n + 1);
      } catch (error) {
        console.error('Failed to sync pending vouch:', record.subjectKeyID, error);
      }
    }
  },
};
