import { authService } from '$lib/services/auth';
import { cryptoService } from '$lib/services/crypto';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
import { revocationRepository } from '$lib/repositories/revocation';

export type ProfileKeyInfo = {
  keyId: string;
  identity: string;
  armor: string;
  isPendingRevocation: boolean;
  isKeyRevoked: boolean;
  revokedInfo: { reason: string; timestamp: string; successor: string | null } | null;
};

/** Resolve the active key's armor, identity, and revocation state from local stores. */
export async function loadProfileKeyInfo(): Promise<ProfileKeyInfo> {
  const keyId = authService.getActiveKeyId();
  if (!keyId) {
    throw new Error('Active key id is missing');
  }

  const publicKey = await publicKeyRepository.getPublicKey(keyId);
  if (!publicKey) {
    throw new Error(`Public key not found for key id ${keyId}`);
  }

  const identity = await cryptoService.getKeyIdentity(publicKey.armor);
  let isKeyRevoked = publicKey.revoked;
  let revokedInfo: ProfileKeyInfo['revokedInfo'] = null;

  if (publicKey.revoked) {
    const revocation = await revocationRepository.get(keyId);
    if (revocation) {
      revokedInfo = {
        reason: revocation.reason,
        timestamp: revocation.serverSignature.timestamp,
        successor: revocation.successor,
      };
    }
  }

  const isPendingRevocation = !!(await pendingRevocationRepository.get(keyId));
  if (isPendingRevocation) isKeyRevoked = true;

  return {
    keyId,
    identity,
    armor: publicKey.armor,
    isPendingRevocation,
    isKeyRevoked,
    revokedInfo,
  };
}
