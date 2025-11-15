import { dbService, type DbService } from '../services/db';

export interface PrivateKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
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
    return keyData !== null;
  }

  /**
   * Delete a private key for a user
   */
  async deletePrivateKey(fingerprint: string): Promise<void> {
    await this.db.delete('privateKeys', fingerprint);
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
