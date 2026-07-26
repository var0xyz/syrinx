import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned, diagnosePublicKey } from '$lib/verifiers';

/**
 * Local public-key cache. Records are the full wire `PublicKey` shape
 * (including `serverSignature` and boolean `revoked`) — never armor-only stubs.
 */
export class PublicKeyRepository {
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  /**
   * Persist a server-attested public key. Verification runs before the
   * IndexedDB write so failure reasons (signature_invalid, etc.) reach
   * the signup UI. If we already hold this fingerprint, refuse to
   * overwrite with different armor.
   */
  async put(key: api.PublicKey): Promise<void> {
    const existing = await this.getPublicKey(key.fingerprint);
    if (existing && existing.armor !== key.armor) {
      throw new Error(
        `Refusing to overwrite locally-cached public key ${key.fingerprint}: ` +
          'server-returned armor does not match the one already on file'
      );
    }

    const diagnosis = await diagnosePublicKey(key);
    if (diagnosis.ok === false) {
      const detail = diagnosis.detail ? ` (${diagnosis.detail})` : '';
      throw new Error(
        `Refusing to store in publicKeys: verification failed: ${diagnosis.reason}${detail}`
      );
    }

    // Already diagnosed above — skip a second verify pass in dbService.put.
    await this.db.put('publicKeys', { ...key, armor: key.armor }, allowUnsigned);
  }

  async getPublicKey(fingerprint: string): Promise<api.PublicKey | null> {
    return await this.db.get<api.PublicKey>('publicKeys', fingerprint);
  }

  async hasPublicKey(fingerprint: string): Promise<boolean> {
    return !!(await this.getPublicKey(fingerprint));
  }

  async deletePublicKey(fingerprint: string): Promise<void> {
    return await this.db.delete('publicKeys', fingerprint);
  }

  /**
   * Persist the server's view of a revoked key (revoked: true only).
   * Revocation details are stored separately in the revocations store.
   */
  async setRevoked(revokedKey: api.PublicKey): Promise<void> {
    if (!revokedKey.revoked) {
      throw new Error(
        `setRevoked called without revoked=true for: ${revokedKey.fingerprint}`
      );
    }
    await this.put(revokedKey);
  }

  async listPublicKeys(): Promise<api.PublicKey[]> {
    return await this.db.getAll<api.PublicKey>('publicKeys');
  }

  async clearAllPublicKeys(): Promise<void> {
    await this.db.clear('publicKeys');
  }
}

export const publicKeyRepository = new PublicKeyRepository();

/** @deprecated Import from `$lib/verifiers` instead. */
export { verifyPublicKey } from '$lib/verifiers';
