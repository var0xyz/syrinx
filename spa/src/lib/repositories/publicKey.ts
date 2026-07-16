import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { cryptoService } from '../services/crypto';
import { buildPublicKeyPayload } from '../services/signing';
import { signedAtHeader, verify } from '../services/verify';

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

/** Rebuild payload + verify server signature + armor↔fingerprint. */
export async function verifyPublicKey(key: api.PublicKey): Promise<boolean> {
  const result = await verify(
    key.server,
    buildPublicKeyPayload(
      key.userID,
      key.fingerprint,
      key.server.id,
      key.server.fingerprint,
      key.armor,
      signedAtHeader(key.server.timestamp)
    )
  );
  if (result.ok === false) {
    console.error('[verifyPublicKey] server signature failed', result);
    return false;
  }
  const derived = await cryptoService.fingerprintFromArmor(key.armor);
  if (derived.toLowerCase() !== key.fingerprint.toLowerCase()) {
    console.error('[verifyPublicKey] fingerprint mismatch', { labeled: key.fingerprint, derived });
    return false;
  }
  return true;
}

export class PublicKeyRepository {
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  async put(fingerprint: string, armor: string): Promise<void> {
    await this.db.put('publicKeys', {
      fingerprint,
      armor: armor.trim(),
      createdAt: new Date(),
    });
  }

  async getPublicKey(fingerprint: string): Promise<PublicKey | null> {
    return await this.db.get<PublicKey>('publicKeys', fingerprint);
  }

  async hasPublicKey(fingerprint: string): Promise<boolean> {
    return !!(await this.getPublicKey(fingerprint));
  }

  async deletePublicKey(fingerprint: string): Promise<void> {
    await this.db.delete('publicKeys', fingerprint);
  }

  /**
   * Mark a locally-cached public key as revoked, using the server's
   * response as the source of truth for the revocation metadata.
   *
   * Refuse to overwrite armor if it doesn't match what we already have —
   * a hostile server could return different key material under the same
   * fingerprint label. Legitimate revocations ship byte-identical armor.
   */
  async setRevoked(fingerprint: string, revokedKey: api.PublicKey): Promise<void> {
    const existing = await this.getPublicKey(fingerprint);
    if (!existing) throw new Error(`Public key not found: ${fingerprint}`);

    if (!(await verifyPublicKey(revokedKey))) {
      return;
    }

    if (existing.armor !== revokedKey.armor) {
      console.error(
        '[publicKeyRepository.setRevoked] Refusing to overwrite locally-cached public key: ' +
        'server-returned armor does not match the one already on file for this fingerprint.',
        {
          fingerprint,
          localArmorLength: existing.armor.length,
          remoteArmorLength: revokedKey.armor.length,
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

  async clearAllPublicKeys(): Promise<void> {
    await this.db.clear('publicKeys');
  }
}

export const publicKeyRepository = new PublicKeyRepository();
