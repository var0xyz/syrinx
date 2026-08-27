import { writable } from 'svelte/store';
import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { authService } from '$lib/services/auth';
import { requestSigner } from '$lib/services/request-signer';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { revocationRepository } from '$lib/repositories/revocation';
import { allowUnsigned } from '$lib/verifiers';

export interface PendingRevocationRecord {
  fingerprint: string;          // old key — keyPath
  reason: string;
  userId: string;
  newFingerprint: string;
  newPublicKey: string;         // armored
  userRevocationSignature: string; // base64 user sig over revocation payload
  revokedKeySignature: string;  // rotation proof: old key signs new armor
  newKeySignature: string;
  keyRevoked?: boolean;         // true once revokeKey() has been confirmed by the server
}

// Incremented after each successful sync so subscribers can react
export const pendingRevocationSynced = writable(0);

export const pendingRevocationRepository = {
  async put(record: PendingRevocationRecord): Promise<void> {
    await dbService.put('pendingRevocation', record, allowUnsigned);
  },

  async delete(fingerprint: string): Promise<void> {
    await dbService.delete('pendingRevocation', fingerprint);
  },

  async markRevoked(fingerprint: string): Promise<void> {
    const existing = await dbService.get<PendingRevocationRecord>('pendingRevocation', fingerprint);
    if (!existing) throw new Error(`Pending revocation not found: ${fingerprint}`);
    await dbService.put(
      'pendingRevocation',
      { ...existing, keyRevoked: true },
      allowUnsigned
    );
  },

  async get(fingerprint: string): Promise<PendingRevocationRecord | null> {
    return dbService.get<PendingRevocationRecord>('pendingRevocation', fingerprint);
  },

  async getAll(): Promise<PendingRevocationRecord[]> {
    return dbService.getAll<PendingRevocationRecord>('pendingRevocation');
  },

  async syncPending(): Promise<void> {
    const pending = await dbService.getAll<PendingRevocationRecord>('pendingRevocation');
    for (const record of pending) {
      try {
        if (!record.keyRevoked) {
          const revokedKey = await apiService.revokeKey(
            record.userId,
            record.fingerprint,
            record.reason,
            record.userRevocationSignature
          );
          await publicKeyRepository.setRevoked(revokedKey);
          await privateKeyRepository.setRevoked(record.fingerprint);
          await pendingRevocationRepository.markRevoked(record.fingerprint);
        }

        // addPublicKey isn't safely repeatable — a prior sync attempt may
        // have already registered the new key before failing on a LATER
        // step (e.g. fetching the revocation certificate below used to be
        // able to fail this whole record, from before this fix). Treat a
        // 409 here as "already done", not an error: fetch the
        // already-registered key instead of retrying the write.
        let newPublicKey;
        try {
          newPublicKey = await apiService.addPublicKey(
            record.userId,
            btoa(record.newPublicKey),
            record.fingerprint,
            record.revokedKeySignature,
            record.newKeySignature
          );
        } catch (addKeyError) {
          const status = (addKeyError as { status?: number })?.status;
          if (status !== 409) throw addKeyError;
          newPublicKey = await apiService.getPublicKey(record.newFingerprint);
        }
        await publicKeyRepository.put(newPublicKey);

        // The rotation is fully done server-side at this point — clear the
        // pending record now, before touching the active key or fetching
        // the revocation certificate below. Neither of those can safely
        // re-trigger revokeKey/addPublicKey on a later retry (they already
        // succeeded), so this record must not still exist to be retried
        // from scratch if either step below fails.
        await dbService.delete('pendingRevocation', record.fingerprint);
        pendingRevocationSynced.update(n => n + 1);

        authService.setActiveKey(record.newFingerprint);
        const passphrase = authService.getPassphrase();
        if (passphrase) await requestSigner.initializeWorker(record.newFingerprint, passphrase);

        // Best-effort: fetch the server-countersigned revocation proof for
        // local caching. Failure here is not retried via this record — the
        // rotation already succeeded — so it's logged, not rethrown.
        try {
          const revocation = await apiService.getKeyRevocation(record.userId, record.fingerprint);
          await revocationRepository.put(revocation);
        } catch (revocationFetchError) {
          console.error('Failed to fetch revocation certificate (rotation already complete):', record.fingerprint, revocationFetchError);
        }
      } catch (error) {
        console.error('Failed to sync pending revocation:', record.fingerprint, error);
      }
    }
  },
};
