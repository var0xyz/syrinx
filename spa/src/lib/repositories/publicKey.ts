import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';

export interface PublicKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
  revoked?: {
    reason: string;
    timestamp: string;
    successor: string | null;
  } | null;
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

  /**
   * Mark a locally-cached public key as revoked, using the server's
   * response as the source of truth for the revocation metadata.
   *
   * Security check — refuse to overwrite the armor if it doesn't match
   * what we already have. The threat: a compromised or hostile server
   * could return a *different* key body under the same fingerprint
   * (say, a key it controls) with `revoked: true`, hoping the client
   * naively replaces its trusted local copy. Since the fingerprint is
   * derived from the key material, a legitimate revocation of the key
   * we already hold must ship back byte-identical armor. If it doesn't,
   * we abort without touching local state.
   *
   * For now we only log to the console; future work should surface this
   * to the user as a "your server may be tampering with your keys"
   * warning, because in practice this indicates either (a) a bug in the
   * server, (b) a bug in our local storage, or (c) an active attack.
   */
  async setRevoked(fingerprint: string, revokedKey: api.PublicKey): Promise<void> {
    const existing = await this.getPublicKey(fingerprint);
    if (!existing) throw new Error(`Public key not found: ${fingerprint}`);

    if (existing.armor !== revokedKey.armor) {
      console.error(
        '[publicKeyRepository.setRevoked] Refusing to overwrite locally-cached public key: ' +
        'server-returned armor does not match the one already on file for this fingerprint. ' +
        'This should never happen for a legitimate revocation (fingerprint is derived from ' +
        'the key material) and may indicate a hostile or misbehaving server.',
        {
          fingerprint,
          localArmorLength: existing.armor.length,
          remoteArmorLength: revokedKey.armor.length,
          localArmorPreview: existing.armor.slice(0, 80),
          remoteArmorPreview: revokedKey.armor.slice(0, 80),
        }
      );
      return;
    }

    await this.db.put('publicKeys', {
      ...existing,
      revoked: revokedKey.revoked ?? null,
    });
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
