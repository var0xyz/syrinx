import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { verifyPublicKey } from '$lib/verifiers';

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
   * Persist a server-attested public key. Verification runs inside
   * `dbService.put` via `verifyPublicKey`. If we already hold this id,
   * refuse to overwrite with different armor.
   *
   * `fingerprint` duplicates `id` (the canonical key id) on the stored
   * record only — the IndexedDB store's keyPath is still the literal
   * property name `fingerprint` (see db.ts's v10 comment), while the wire
   * type names that same field `id`.
   */
  async put(key: api.PublicKey): Promise<void> {
    const existing = await this.getPublicKey(key.id);
    if (existing && existing.armor !== key.armor) {
      throw new Error(
        `Refusing to overwrite locally-cached public key ${key.id}: ` +
          'server-returned armor does not match the one already on file'
      );
    }

    const record = { ...key, fingerprint: key.id };
    await this.db.put('publicKeys', record, verifyPublicKey);
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
        `setRevoked called without revoked=true for: ${revokedKey.id}`
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
