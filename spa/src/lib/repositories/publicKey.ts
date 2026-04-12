import { dbService, type DbService } from '../services/db';

export interface PublicKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
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
