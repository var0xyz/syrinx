/**
 * Own-identity claim during SPA recovery.
 * Challenge-response + nested key chain; initializes request signing on success.
 */

import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { dbService } from './db';
import { buildKeyNest } from './recoveryKeyNest';
import { requestSigner } from './request-signer';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { appendFingerprint } from '$lib/utils/keyId';

/**
 * Claim the restored owner's identity on a recovery-mode server.
 * Does not clear recoveryRun — mid-recovery session stays gated.
 */
export async function claimOwnIdentity(): Promise<api.User> {
  const userId = localStorage.getItem('userId');
  if (!userId) {
    throw new Error('Missing local userId; restore a backup first.');
  }

  const fingerprint = authService.getActiveKeyFingerprint();
  const passphrase = authService.getPassphrase();
  if (!fingerprint || !passphrase) {
    throw new Error('Missing active key or passphrase after restore.');
  }

  await dbService.init();
  const [users, usersInfo, publicKeys, revocations] = await Promise.all([
    dbService.getAll<api.User>('users'),
    dbService.getAll<api.UserInfo>('usersInfo'),
    dbService.getAll<api.PublicKey>('publicKeys'),
    dbService.getAll<api.KeyRevocation>('revocations'),
  ]);

  const usersById = new Map(users.map((u) => [u.id, u]));
  const infoByUserId = new Map(usersInfo.map((i) => [i.id, i]));
  const keysByFp = new Map(
    publicKeys.map((k) => [k.id.toLowerCase(), k])
  );
  const revocationsByFp = new Map(
    revocations.map((r) => [r.id.toLowerCase(), r])
  );

  const profile = usersById.get(userId);
  if (!profile) {
    throw new Error('Missing restored profile for claim.');
  }
  const infoFp =
    infoByUserId.get(userId)?.activeKeyFingerprint ||
    (profile as api.User & { activeKeyFingerprint?: string }).activeKeyFingerprint;
  if (!infoFp || fingerprint.toLowerCase() !== infoFp.toLowerCase()) {
    throw new Error(
      'Active key fingerprint does not match the restored profile.'
    );
  }

  const nest = buildKeyNest(userId, {
    getUser: (id) => usersById.get(id),
    getActiveKeyFingerprint: (id) =>
      infoByUserId.get(id)?.activeKeyFingerprint ||
      (usersById.get(id) as api.User & { activeKeyFingerprint?: string } | undefined)
        ?.activeKeyFingerprint,
    getPublicKey: (fp) => keysByFp.get(fp.toLowerCase()),
    getRevocation: (fp) => revocationsByFp.get(fp.toLowerCase()) ?? null,
  });
  if (nest.ok === false) {
    throw new Error(`Incomplete key nest: ${nest.reason}`);
  }

  // Challenge must be signed by the nest outermost key (server verifies that).
  // nest.key.fingerprint is bare (recoveryKeyNest.ts builds wire nodes bare,
  // matching the recovery package's wire/verification exception); the
  // privateKeys lookup wants the canonical form.
  const activeBareFingerprint = nest.key.fingerprint;
  const activeFingerprint = appendFingerprint(userId, activeBareFingerprint);
  const privateKey = await privateKeyRepository.getPrivateKey(activeFingerprint);
  if (!privateKey?.armor) {
    throw new Error('Missing private key for active fingerprint.');
  }

  const { challenge } = await apiService.getIdentityClaimChallenge();
  const sigArmor = await cryptoService.signMessage(
    String(challenge),
    privateKey.armor,
    passphrase
  );
  const signature = btoa(sigArmor);

  const claimed = await apiService.claimOwnIdentity({
    challenge,
    signature,
    profile,
    key: nest.key,
  });

  await authService.saveUserToStorage(claimed);
  authService.setActiveKey(activeFingerprint);
  await requestSigner.initializeWorker(activeFingerprint, passphrase);

  return claimed;
}
