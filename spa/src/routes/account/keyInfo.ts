import { authService } from '$lib/services/auth';
import { cryptoService } from '$lib/services/crypto';
import { publicKeyRepository } from '$lib/repositories/publicKey';
import { pendingRevocationRepository } from '$lib/repositories/pendingRevocation';
import { revocationRepository } from '$lib/repositories/revocation';

export type ProfileKeyInfo = {
  fingerprint: string;
  identity: string;
  armor: string;
  isPendingRevocation: boolean;
  isKeyRevoked: boolean;
  revokedInfo: { reason: string; timestamp: string; successor: string | null } | null;
};

/** Resolve the active key's armor, identity, and revocation state from local stores. */
export async function loadProfileKeyInfo(): Promise<ProfileKeyInfo> {
  const fingerprint = authService.getActiveKeyFingerprint();
  if (!fingerprint) {
    throw new Error('Active key fingerprint is missing');
  }

  const publicKey = await publicKeyRepository.getPublicKey(fingerprint);
  if (!publicKey) {
    throw new Error(`Public key not found for fingerprint ${fingerprint}`);
  }

  const identity = await cryptoService.getKeyIdentity(publicKey.armor);
  let isKeyRevoked = publicKey.revoked;
  let revokedInfo: ProfileKeyInfo['revokedInfo'] = null;

  if (publicKey.revoked) {
    const revocation = await revocationRepository.get(fingerprint);
    if (revocation) {
      revokedInfo = {
        reason: revocation.reason,
        timestamp: revocation.serverSignature.timestamp,
        successor: revocation.successor,
      };
    }
  }

  const isPendingRevocation = !!(await pendingRevocationRepository.get(fingerprint));
  if (isPendingRevocation) isKeyRevoked = true;

  return {
    fingerprint,
    identity,
    armor: publicKey.armor,
    isPendingRevocation,
    isKeyRevoked,
    revokedInfo,
  };
}
