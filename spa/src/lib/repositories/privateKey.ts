import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { appendFingerprint, parseKeyId } from '$lib/utils/keyId';

/**
 * Local private-key material. Revocation *attestation* (reason, timestamp,
 * successor, server countersignature) lives on the matching public key —
 * that record is what peers verify. Here `revoked` is only a local flag
 * so we do not keep treating this material as an active signing key.
 */
export interface PrivateKey {
  fingerprint: string;
  armor: string;
  createdAt: Date;
  revoked: boolean;
}

export class PrivateKeyRepository {
  private db = dbService;

  async put(fingerprint: string, armor: string): Promise<void> {
    const now = new Date();
    const keyData: PrivateKey = {
      fingerprint,
      armor: armor.trim(),
      createdAt: now,
      revoked: false,
    };

    await this.db.put('privateKeys', keyData, allowUnsigned);
  }

  async getPrivateKey(fingerprint: string): Promise<PrivateKey | null> {
    return await this.db.get<PrivateKey>('privateKeys', fingerprint);
  }

  /** When this key was minted (put into IndexedDB) — `__meta__.created`, in ms. */
  async getMintedAt(fingerprint: string): Promise<number | null> {
    const meta = await this.db.getMeta('privateKeys', fingerprint);
    return meta?.created ?? null;
  }

  async hasPrivateKey(fingerprint: string): Promise<boolean> {
    const keyData = await this.getPrivateKey(fingerprint);
    return !!keyData;
  }

  async deletePrivateKey(fingerprint: string): Promise<void> {
    await this.db.delete('privateKeys', fingerprint);
  }

  /** Mark local private-key material as revoked (boolean flag only). */
  async setRevoked(fingerprint: string): Promise<void> {
    const existing = await this.getPrivateKey(fingerprint);
    if (!existing) throw new Error(`Private key not found: ${fingerprint}`);
    await this.db.put('privateKeys', { ...existing, revoked: true }, allowUnsigned);
  }

  async listPrivateKeys(): Promise<Omit<PrivateKey, 'armor'>[]> {
    const allKeys = await this.db.getAll<PrivateKey>('privateKeys');
    return allKeys.map(({ armor, ...keyInfo }) => keyInfo);
  }
}

export const privateKeyRepository = new PrivateKeyRepository();

/**
 * One-time v10 migration: re-key every 'privateKeys' row from a bare
 * fingerprint to the canonical userID@serverID/fingerprint form (see db.ts's
 * v10 comment). Must run once per client, after dbService.init()'s store
 * upgrade has completed — called from the app bootstrap load function.
 *
 * A privateKeys row doesn't carry its own owning userID (unlike, say,
 * publicKeys' wire shape), so this assumes every row on this client belongs
 * to the currently signed-in user (localStorage 'userId', already
 * canonical) — true today since there's no multi-account switching in this
 * app. Rows whose keyPath already looks canonical (a prior run, or a fresh
 * v10+ client) are left untouched.
 */
export async function migratePrivateKeyFingerprintsV10(canonicalUserId: string): Promise<void> {
  const rows = await dbService.getAll<PrivateKey>('privateKeys');
  for (const row of rows) {
    if (parseKeyId(row.fingerprint)) continue; // already canonical
    const canonicalFingerprint = appendFingerprint(canonicalUserId, row.fingerprint);
    await dbService.delete('privateKeys', row.fingerprint);
    await dbService.put('privateKeys', { ...row, fingerprint: canonicalFingerprint }, allowUnsigned);
  }
}
