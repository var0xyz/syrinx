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
  keyId: string;                // old key — keyPath
  reason: string;
  userId: string;
  newKeyId: string;
  newPublicKey: string;         // armored
  userRevocationSignature: string; // base64 user sig over revocation payload
  revokedKeySignature: string;  // rotation proof: old key signs new armor
  newKeySignature: string;
}

// Incremented after each successful sync so subscribers can react
export const pendingRevocationSynced = writable(0);

export const pendingRevocationRepository = {
  async put(record: PendingRevocationRecord): Promise<void> {
    await dbService.put('pendingRevocation', record, allowUnsigned);
  },

  async delete(keyId: string): Promise<void> {
    await dbService.delete('pendingRevocation', keyId);
  },

  async get(keyId: string): Promise<PendingRevocationRecord | null> {
    return dbService.get<PendingRevocationRecord>('pendingRevocation', keyId);
  },

  async getAll(): Promise<PendingRevocationRecord[]> {
    return dbService.getAll<PendingRevocationRecord>('pendingRevocation');
  },

  async syncPending(): Promise<void> {
    const pending = await dbService.getAll<PendingRevocationRecord>('pendingRevocation');
    for (const record of pending) {
      try {
        // addPublicKey isn't safely repeatable — a prior sync attempt may
        // have already registered the new key before failing on a LATER
        // step below. Treat a 409 here as "already done", not an error:
        // fetch the already-registered key instead of retrying the write.
        let newPublicKey;
        try {
          newPublicKey = await apiService.addPublicKey(
            record.userId,
            btoa(record.newPublicKey),
            record.keyId,
            record.revokedKeySignature,
            record.newKeySignature,
            record.reason,
            record.userRevocationSignature
          );
        } catch (addKeyError) {
          const status = (addKeyError as { status?: number })?.status;
          if (status !== 409) throw addKeyError;
          newPublicKey = await apiService.getPublicKey(record.newKeyId);
        }

        // The rotation is fully done server-side at this point (old key
        // revoked, new key registered, atomically) — clear the pending
        // record and switch the active key BEFORE storing/verifying the
        // new key locally. Verifying the new key fetches the predecessor's
        // revocation cert over a signed request; that request must be
        // signed with the new key, not the now-revoked old one.
        await dbService.delete('pendingRevocation', record.keyId);
        pendingRevocationSynced.update(n => n + 1);
        await privateKeyRepository.setRevoked(record.keyId);
        authService.setActiveKey(record.newKeyId);
        const passphrase = authService.getPassphrase();
        if (passphrase) await requestSigner.initializeWorker(record.newKeyId, passphrase);

        await publicKeyRepository.put(newPublicKey);

        // Best-effort: fetch the now-revoked old key's updated record and
        // the server-countersigned revocation proof for local caching.
        // Failure here is not retried via this record — the rotation
        // already succeeded — so it's logged, not rethrown.
        try {
          const revokedKey = await apiService.getPublicKey(record.keyId);
          await publicKeyRepository.setRevoked(revokedKey);
          const revocation = await apiService.getKeyRevocation(record.userId, record.keyId);
          await revocationRepository.put(revocation);
        } catch (revocationFetchError) {
          console.error('Failed to fetch revocation certificate (rotation already complete):', record.keyId, revocationFetchError);
        }
      } catch (error) {
        console.error('Failed to sync pending revocation:', record.keyId, error);
      }
    }
  },
};
