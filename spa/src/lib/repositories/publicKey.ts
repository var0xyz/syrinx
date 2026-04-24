import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';

export interface PublicKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
  revoked?: { reason: string; timestamp: string } | null;
}

export class PublicKeyRepository {
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  /**
   * Store a public key for a user
   */
  async put(fingerprint: string, armor: string): Promise<void> {
    const now = new Date();
    const keyData: PublicKey = {
      fingerprint,
      armor: armor.trim(),
      createdAt: now,
    };

    await this.db.put('publicKeys', keyData);
  }

  /**
   * Retrieve a public key for a user
   */
  async getPublicKey(fingerprint: string): Promise<PublicKey | null> {
    return await this.db.get<PublicKey>('publicKeys', fingerprint);
  }

  /**
   * Check if a public key exists for a user
   */
  async hasPublicKey(fingerprint: string): Promise<boolean> {
    const keyData = await this.getPublicKey(fingerprint);
    return !!keyData;
  }

  /**
   * Delete a public key for a user
   */
  async deletePublicKey(fingerprint: string): Promise<void> {
    await this.db.delete('publicKeys', fingerprint);
  }

  async setRevoked(fingerprint: string, revokedKey: api.PublicKey): Promise<void> {
    const existing = await this.getPublicKey(fingerprint);
    if (!existing) throw new Error(`Public key not found: ${fingerprint}`);
    await this.db.put('publicKeys', { ...existing, armor: revokedKey.armor, revoked: true });
  }

  async listPublicKeys(): Promise<PublicKey[]> {
    return await this.db.getAll<PublicKey>('publicKeys');
  }

  /**
   * Clear all public keys (use with caution!)
   */
  async clearAllPublicKeys(): Promise<void> {
    await this.db.clear('publicKeys');
  }
}

export const publicKeyRepository = new PublicKeyRepository();
