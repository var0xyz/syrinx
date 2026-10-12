import { apiService } from './api';
import { cryptoService } from './crypto';
import { unwrapArmor, wrapArmor } from './keyVault';
import { restoreFromIdentityBackup } from './accountRecovery';
import { buildUserRevocationPayload } from './signing';
import { appendFingerprint } from '$lib/utils/identityRef';
import { pendingRebindRepository, type PendingRebindRecord } from '$lib/repositories/pendingRebind';

/** A user ID with this server's ID appended when it was typed bare. */
export function canonicalRebindUserID(typed: string, serverID: string): string {
  const id = typed.trim();
  return id.includes('@') ? id : `${id}@${serverID}`;
}

/** Mints the key this device will hold once admins bind it. No network. */
export async function mintPendingRebind(userID: string): Promise<PendingRebindRecord> {
  const pair = await cryptoService.generateKeyPair({ name: userID });
  const record: PendingRebindRecord = {
    userID,
    keyID: appendFingerprint(userID, pair.fingerprint),
    fingerprint: pair.fingerprint,
    publicKey: pair.publicKey,
    privateKey: await wrapArmor(pair.privateKey),
    createdAt: Date.now(),
  };
  await pendingRebindRepository.put(record);
  return record;
}

/** `<userID>.pub.txt.gz`: the armored public key, gzip-compressed. */
export async function rebindFile(record: PendingRebindRecord): Promise<File> {
  const stream = new Blob([record.publicKey]).stream().pipeThrough(new CompressionStream('gzip'));
  const bytes = await new Response(stream).blob();
  return new File([bytes], `${record.userID}.pub.txt.gz`, { type: 'application/gzip' });
}

/** The armored key inside a `<userID>.pub.txt.gz` an admin uploads, after
 * checking the filename names userID. */
export async function readRebindFile(file: File, userID: string): Promise<string> {
  if (file.name !== `${userID}.pub.txt.gz`) {
    throw new Error(`This file isn’t for this user: expected ${userID}.pub.txt.gz`);
  }
  const stream = file.stream().pipeThrough(new DecompressionStream('gzip'));
  return (await new Response(stream).text()).trim();
}

/** Rotates away from the rebound key once admins approved it, then signs
 * in like a keys-only recovery. False while it isn't bound yet. */
export async function finishRebind(record: PendingRebindRecord): Promise<boolean> {
  const restrictedKey = await unwrapArmor(record.privateKey);
  const sign = async (payload: string, key: string) => (await cryptoService.signMessage(payload, key, '')).trim();
  if (!record.fresh) {
    const pair = await cryptoService.generateKeyPair({ name: record.userID });
    record.fresh = {
      keyID: appendFingerprint(record.userID, pair.fingerprint),
      publicKey: pair.publicKey,
      privateKey: await wrapArmor(pair.privateKey),
    };
    await pendingRebindRepository.put(record);
  }
  const fresh = record.fresh;
  const freshPrivateKey = await unwrapArmor(fresh.privateKey);

  if (!record.rotated) {
    try {
      record.rotated = await apiService.addPublicKey(
        record.userID,
        fresh.publicKey,
        record.keyID,
        await sign(fresh.publicKey, restrictedKey),
        await sign(fresh.publicKey, freshPrivateKey),
        'rotated',
        await sign(buildUserRevocationPayload(record.userID, record.keyID, 'rotated'), restrictedKey)
      );
    } catch (err) {
      if ((err as { status?: number })?.status === 404) return false;
      throw err;
    }
    await pendingRebindRepository.put(record);
  }

  await restoreFromIdentityBackup({
    localStorage: { userId: record.userID, activeKeyId: fresh.keyID, serverId: record.userID.split('@').pop()! },
    indexedDB: {
      tables: [
        { name: 'privateKeys', items: [{ keyId: fresh.keyID, armor: freshPrivateKey }] },
        { name: 'publicKeys', items: [record.rotated as object] },
      ],
    },
  });
  // A lost device means no backup: the mandatory backup prompt must show.
  localStorage.removeItem('lastKeyBackupAt');
  await pendingRebindRepository.delete(record.userID);
  return true;
}
