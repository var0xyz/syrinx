import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { apiService } from '../services/api';
import { cryptoService } from '../services/crypto';
import { buildPublicKeyPayload } from '../services/signing';
import { signedAtHeader, verify } from '../services/verify';

async function getPredecessor(
  key: api.PublicKey,
  cache: Pick<PublicKeyRepository, 'getPublicKey'>
): Promise<api.PublicKey | null> {
  const predFp = key.predecessor?.fingerprint;
  if (!predFp) return null;

  const cached = await cache.getPublicKey(predFp);
  if (cached) return cached;

  try {
    return await apiService.getPublicKey(key.userID, predFp);
  } catch {
    return null;
  }
}

/** Rebuild payload + verify server signature + armor↔fingerprint. */
export async function verifyPublicKey(
  key: api.PublicKey,
  cache: Pick<PublicKeyRepository, 'getPublicKey'>
): Promise<boolean> {
  if (!key?.serverSignature) {
    console.error('[verifyPublicKey] missing serverSignature block', key?.fingerprint);
    return false;
  }
  const result = await verify(
    key.serverSignature,
    buildPublicKeyPayload(
      key.userID,
      key.fingerprint,
      key.serverSignature.serverID,
      key.serverSignature.fingerprint,
      key.armor,
      signedAtHeader(key.serverSignature.timestamp)
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

  if (key.predecessor?.signature) {
    if (!key.predecessor.fingerprint) {
      console.error('[verifyPublicKey] predecessor block missing fingerprint');
      return false;
    }
    const predecessor = await getPredecessor(key, cache);
    if (!predecessor?.armor) {
      console.error('[verifyPublicKey] predecessor public key unavailable', key.predecessor.fingerprint);
      return false;
    }
    const handoffValid = await cryptoService.verifySignature(
      key.armor,
      key.predecessor.signature,
      predecessor.armor
    );
    if (!handoffValid) {
      console.error('[verifyPublicKey] predecessor handoff signature failed', key.fingerprint);
      return false;
    }
  }

  return true;
}

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
   * Persist a server-attested public key. Verifies the countersignature
   * first. If we already hold this fingerprint, refuse to overwrite with
   * different armor (hostile-server defense).
   */
  async put(key: api.PublicKey): Promise<void> {
    if (!(await verifyPublicKey(key, this))) {
      throw new Error(
        `Refusing to store public key ${key.fingerprint}: server signature invalid`
      );
    }

    const existing = await this.getPublicKey(key.fingerprint);
    if (existing && existing.armor !== key.armor) {
      throw new Error(
        `Refusing to overwrite locally-cached public key ${key.fingerprint}: ` +
          'server-returned armor does not match the one already on file'
      );
    }

    await this.db.put('publicKeys', { ...key, armor: key.armor });
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
