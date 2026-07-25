import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/**
 * Local private-key material. Revocation *attestation* (reason, timestamp,
 * successor, server countersignature) lives on the matching public key —
 * that record is what peers verify. Here `revoked` is only a local flag
 * so we do not keep treating this material as an active signing key.
 */
export interface PrivateKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
  revoked: boolean;
}

export class PrivateKeyRepository {
  private db = dbService;

  async put(fingerprint: string, armor: string): Promise<void> {
    const now = new Date();
    const keyData: PrivateKey = {
      fingerprint,
      armor: armor.trim(),
      createdAt: now,
      revoked: false,
    };

    await this.db.put('privateKeys', keyData, allowUnsigned);
  }

  async getPrivateKey(fingerprint: string): Promise<PrivateKey | null> {
    return await this.db.get<PrivateKey>('privateKeys', fingerprint);
  }

  async hasPrivateKey(fingerprint: string): Promise<boolean> {
    const keyData = await this.getPrivateKey(fingerprint);
    return !!keyData;
  }

  async deletePrivateKey(fingerprint: string): Promise<void> {
    await this.db.delete('privateKeys', fingerprint);
  }

  /** Mark local private-key material as revoked (boolean flag only). */
  async setRevoked(fingerprint: string): Promise<void> {
    const existing = await this.getPrivateKey(fingerprint);
    if (!existing) throw new Error(`Private key not found: ${fingerprint}`);
    await this.db.put('privateKeys', { ...existing, revoked: true }, allowUnsigned);
  }

  async listPrivateKeys(): Promise<Omit<PrivateKey, 'armor'>[]> {
    const allKeys = await this.db.getAll<PrivateKey>('privateKeys');
    return allKeys.map(({ armor, ...keyInfo }) => keyInfo);
  }
}

export const privateKeyRepository = new PrivateKeyRepository();
