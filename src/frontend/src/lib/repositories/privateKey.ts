import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { wrapArmor, unwrapArmor } from '$lib/services/keyVault';

/**
 * Local private-key material. Revocation *attestation* (reason, timestamp,
 * successor, server countersignature) lives on the matching public key —
 * that record is what peers verify. Here `revoked` is only a local flag
 * so we do not keep treating this material as an active signing key.
 */
export interface PrivateKey {
  keyId: string;
  armor: string;
  createdAt: Date;
  revoked: boolean;
}

export class PrivateKeyRepository {
  private db = dbService;

  async put(keyId: string, armor: string): Promise<void> {
    const now = new Date();
    const keyData: PrivateKey = {
      keyId,
      armor: await wrapArmor(armor.trim()),
      createdAt: now,
      revoked: false,
    };

    await this.db.put('privateKeys', keyData, allowUnsigned);
  }

  /** Returns the record with `armor` unwrapped, ready to use. */
  async getPrivateKey(keyId: string): Promise<PrivateKey | null> {
    const stored = await this.db.get<PrivateKey>('privateKeys', keyId);
    if (!stored) return null;
    return { ...stored, armor: await unwrapArmor(stored.armor) };
  }

  /** When this key was minted (put into IndexedDB) — `__meta__.created`, in ms. */
  async getMintedAt(keyId: string): Promise<number | null> {
    const meta = await this.db.getMeta('privateKeys', keyId);
    return meta?.created ?? null;
  }

  async hasPrivateKey(keyId: string): Promise<boolean> {
    return !!(await this.db.get<PrivateKey>('privateKeys', keyId));
  }

  async deletePrivateKey(keyId: string): Promise<void> {
    await this.db.delete('privateKeys', keyId);
  }

  /** Mark local private-key material as revoked (boolean flag only). */
  async setRevoked(keyId: string): Promise<void> {
    // Reads the stored record, not getPrivateKey: its armor must go back
    // wrapped, and unwrapping here would write plaintext.
    const existing = await this.db.get<PrivateKey>('privateKeys', keyId);
    if (!existing) throw new Error(`Private key not found: ${keyId}`);
    await this.db.put('privateKeys', { ...existing, revoked: true }, allowUnsigned);
  }

  async listPrivateKeys(): Promise<Omit<PrivateKey, 'armor'>[]> {
    const allKeys = await this.db.getAll<PrivateKey>('privateKeys');
    return allKeys.map(({ armor, ...keyInfo }) => keyInfo);
  }
}

export const privateKeyRepository = new PrivateKeyRepository();
