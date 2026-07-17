import { dbService, type DbService } from '../services/db';

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
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  /**
   * Store a private key for a user
   */
  async put(fingerprint: string, armor: string): Promise<void> {
    const now = new Date();
    const keyData: PrivateKey = {
      fingerprint,
      armor: armor.trim(),
      createdAt: now,
      revoked: false,
    };

    await this.db.put('privateKeys', keyData);
  }

  /**
   * Retrieve a private key for a user
   */
  async getPrivateKey(fingerprint: string): Promise<PrivateKey | null> {
    return await this.db.get<PrivateKey>('privateKeys', fingerprint);
  }

  /**
   * Check if a private key exists for a user
   */
  async hasPrivateKey(fingerprint: string): Promise<boolean> {
    const keyData = await this.getPrivateKey(fingerprint);
    return !!keyData;
  }

  /**
   * Delete a private key for a user
   */
  async deletePrivateKey(fingerprint: string): Promise<void> {
    await this.db.delete('privateKeys', fingerprint);
  }

  /** Mark local private-key material as revoked (boolean flag only). */
  async setRevoked(fingerprint: string): Promise<void> {
    const existing = await this.getPrivateKey(fingerprint);
    if (!existing) throw new Error(`Private key not found: ${fingerprint}`);
    await this.db.put('privateKeys', { ...existing, revoked: true });
  }

  /**
   * List all stored private keys (without the actual key data for security)
   */
  async listPrivateKeys(): Promise<Omit<PrivateKey, 'armor'>[]> {
    const allKeys = await this.db.getAll<PrivateKey>('privateKeys');
    return allKeys.map(({ armor, ...keyInfo }) => keyInfo);
  }
}

export const privateKeyRepository = new PrivateKeyRepository();
