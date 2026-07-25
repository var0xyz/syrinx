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
        const newPublicKey = await apiService.addPublicKey(
          record.userId,
          record.newPublicKey,
          record.fingerprint,
          record.revokedKeySignature,
          record.newKeySignature
        );
        await publicKeyRepository.put(newPublicKey);

        authService.setActiveKey(record.newFingerprint);
        const passphrase = authService.getPassphrase();
        if (passphrase) await requestSigner.initializeWorker(record.newFingerprint, passphrase);

        const revocation = await apiService.getKeyRevocation(record.userId, record.fingerprint);
        await revocationRepository.put(revocation);

        await dbService.delete('pendingRevocation', record.fingerprint);
        pendingRevocationSynced.update(n => n + 1);
      } catch (error) {
        console.error('Failed to sync pending revocation:', record.fingerprint, error);
      }
    }
  },
};
