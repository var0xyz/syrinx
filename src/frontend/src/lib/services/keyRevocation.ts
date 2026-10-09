import type * as api from '$lib/types/api';
import type { ReedType } from '$lib/types/reed';
import { dbService } from './db';
import { reedRemovalCommitted } from './reedRemoval';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { verifyKeyRevocation } from '$lib/verifiers';
import { reedsSignedAfterRevocation } from '$lib/utils/keyRevocation';

/**
 * Applies a pushed KEY_REVOKED: stores the verified revocation, marks the
 * cached key revoked, and drops local reeds the key signed at or after it.
 * Returns false, changing nothing, when the revocation doesn't verify.
 */
export async function applyKeyRevocation(revocation: api.KeyRevocation): Promise<boolean> {
  try {
    await dbService.put('revocations', revocation, verifyKeyRevocation);
  } catch (error) {
    console.error('[applyKeyRevocation] refused', revocation?.id, error);
    return false;
  }

  const cached = await publicKeyRepository.getPublicKey(revocation.id);
  if (cached && !cached.revoked) {
    await publicKeyRepository.setRevoked({ ...cached, revoked: true });
  }

  const held = await dbService.getAllByIndex<ReedType>('reeds', 'userID', revocation.userId);
  const dropped = reedsSignedAfterRevocation(
    held,
    revocation.id,
    revocation.serverSignature.signedAt
  );
  await Promise.all(dropped.map((reed) => dbService.delete('reeds', reed.id)));
  if (dropped.length > 0) reedRemovalCommitted.update((n) => n + 1);
  return true;
}
