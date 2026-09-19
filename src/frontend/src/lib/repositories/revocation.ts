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

  async put(revocation: api.KeyRevocation): Promise<void> {
    await this.db.put('revocations', revocation, verifyKeyRevocation);
  }

  async get(id: string): Promise<api.KeyRevocation | null> {
    return await this.db.get<api.KeyRevocation>('revocations', id);
  }

  /**
   * Patch successor after AddPublicKey. Successor is unsigned bookkeeping;
   * signatures on the cert are re-checked by verifyKeyRevocation.
   */
  async patchSuccessor(id: string, successor: string): Promise<void> {
    const existing = await this.get(id);
    if (!existing) {
      throw new Error(`Revocation not found: ${id}`);
    }
    await this.put({ ...existing, successor });
  }
}

export const revocationRepository = new RevocationRepository();

/** @deprecated Import from `$lib/verifiers` instead. */
export { verifyKeyRevocation } from '$lib/verifiers';
