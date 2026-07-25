import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { cryptoService } from '../services/crypto';
import { buildServerRevocationPayload, buildUserRevocationPayload } from '../services/signing';
import { signedAtHeader, verify } from '../services/verify';
import { publicKeyRepository } from './publicKey';

/** Verify user + server signatures on a revocation resource. */
export async function verifyKeyRevocation(
  revocation: api.KeyRevocation,
  publicKeyArmor: string
): Promise<boolean> {
  if (!revocation?.serverSignature || !revocation.userSignature?.armor) {
    console.error('[verifyKeyRevocation] missing signatures', revocation?.fingerprint);
    return false;
  }

  const userPayload = buildUserRevocationPayload(
    revocation.userID,
    revocation.fingerprint,
    revocation.reason
  );
  const userValid = await cryptoService.verifySignature(
    userPayload,
    atob(revocation.userSignature.armor),
    publicKeyArmor
  );
  if (!userValid) {
    console.error('[verifyKeyRevocation] user signature failed', revocation.fingerprint);
    return false;
  }

  const serverPayload = buildServerRevocationPayload(
    revocation.userID,
    revocation.fingerprint,
    revocation.reason,
    revocation.serverSignature.serverID,
    revocation.serverSignature.fingerprint,
    revocation.userSignature.armor,
    signedAtHeader(revocation.serverSignature.timestamp)
  );
  const serverResult = await verify(revocation.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyKeyRevocation] server signature failed', serverResult);
    return false;
  }
  return true;
}

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
    const publicKey = await publicKeyRepository.getPublicKey(revocation.fingerprint);
    if (!publicKey?.armor) {
      throw new Error(
        `Refusing to store revocation for ${revocation.fingerprint}: public key armor not cached`
      );
    }
    if (!(await verifyKeyRevocation(revocation, publicKey.armor))) {
      throw new Error(
        `Refusing to store revocation for ${revocation.fingerprint}: signature invalid`
      );
    }
    await this.db.put('revocations', revocation);
  }

  async get(fingerprint: string): Promise<api.KeyRevocation | null> {
    return await this.db.get<api.KeyRevocation>('revocations', fingerprint);
  }

  /**
   * Patch successor after AddPublicKey. Successor is unsigned bookkeeping;
   * a later fetch replaces this with the server value.
   */
  async patchSuccessor(fingerprint: string, successor: string): Promise<void> {
    const existing = await this.get(fingerprint);
    if (!existing) {
      throw new Error(`Revocation not found: ${fingerprint}`);
    }
    await this.db.put('revocations', { ...existing, successor });
  }
}

export const revocationRepository = new RevocationRepository();
