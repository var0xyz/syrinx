import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { verifyKeyRevocation } from '$lib/verifiers';

/**
 * Local cache of signed revocation attestation records. Reason, revoke
 * time, and successor live here — not on the public key wire shape.
 */
export class RevocationRepository {
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  /**
   * `fingerprint` duplicates `revokedId` (the revoked key's canonical id)
   * on the stored record only — the IndexedDB store's keyPath is still the
   * literal property name `fingerprint` (see db.ts's v10 comment), while
   * the wire type names that same field `revokedId`.
   */
  async put(revocation: api.KeyRevocation): Promise<void> {
    const record = { ...revocation, fingerprint: revocation.revokedId };
    await this.db.put('revocations', record, verifyKeyRevocation);
  }

  async get(fingerprint: string): Promise<api.KeyRevocation | null> {
    return await this.db.get<api.KeyRevocation>('revocations', fingerprint);
  }

  /**
   * Patch successor after AddPublicKey. Successor is unsigned bookkeeping;
   * signatures on the cert are re-checked by verifyKeyRevocation.
   */
  async patchSuccessor(fingerprint: string, successor: string): Promise<void> {
    const existing = await this.get(fingerprint);
    if (!existing) {
      throw new Error(`Revocation not found: ${fingerprint}`);
    }
    await this.put({ ...existing, successor });
  }
}

export const revocationRepository = new RevocationRepository();

/** @deprecated Import from `$lib/verifiers` instead. */
export { verifyKeyRevocation } from '$lib/verifiers';
