/**
 * Verifiers for IndexedDB writes. `dbService.put` always requires one of
 * these (or `allowUnsigned`). Entity crypto knowledge lives here — not in
 * dbService or repositories.
 */

import type * as api from '$lib/types/api';
import { reedAsMarkdown, type ReedType } from '$lib/types/reed';
import { apiService } from '$lib/services/api';
import { cryptoService } from '$lib/services/crypto';
import { dbService } from '$lib/services/db';
import {
  buildAccountRemovalServerPayload,
  buildAccountRemovalUserPayload,
  buildProfilePayload,
  buildPublicKeyPayload,
  buildReedPayload,
  buildReedRemovalServerPayload,
  buildReedRemovalUserPayload,
  buildServerRevocationPayload,
  buildUserIdentityPayload,
  buildUserRevocationPayload,
} from '$lib/services/signing';
import { signedAtHeader, verify } from '$lib/services/verify';

export type Verifier<T = unknown> = (data: T) => Promise<boolean>;

/** Explicit no-op for stores that are not signed resources. */
export async function allowUnsigned(_data: unknown): Promise<boolean> {
  return true;
}

async function resolvePublicKeyArmor(
  userID: string,
  fingerprint: string
): Promise<string | null> {
  const cached = await dbService.get<api.PublicKey>('publicKeys', fingerprint);
  if (cached?.armor) return cached.armor;
  try {
    const key = await apiService.getPublicKey(userID, fingerprint);
    if (!key?.armor) return null;
    if (!(await verifyPublicKey(key))) {
      console.error('[resolvePublicKeyArmor] fetched key failed attestation', fingerprint);
      return null;
    }
    return key.armor;
  } catch {
    return null;
  }
}

async function resolvePredecessor(key: api.PublicKey): Promise<api.PublicKey | null> {
  const predFp = key.predecessor?.fingerprint;
  if (!predFp) return null;
  const cached = await dbService.get<api.PublicKey>('publicKeys', predFp);
  if (cached) return cached;
  try {
    return await apiService.getPublicKey(key.userID, predFp);
  } catch {
    return null;
  }
}

/** Server attestation + armor↔fingerprint (+ optional predecessor handoff). */
export async function verifyPublicKey(key: api.PublicKey): Promise<boolean> {
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
    const predecessor = await resolvePredecessor(key);
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

export async function verifyKeyRevocation(revocation: api.KeyRevocation): Promise<boolean> {
  if (!revocation?.serverSignature || !revocation.userSignature?.armor) {
    console.error('[verifyKeyRevocation] missing signatures', revocation?.fingerprint);
    return false;
  }

  // User attestation is signed by the key being revoked.
  const publicKeyArmor = await resolvePublicKeyArmor(revocation.userID, revocation.fingerprint);
  if (!publicKeyArmor) {
    console.error('[verifyKeyRevocation] public key armor unavailable', revocation.fingerprint);
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

export async function verifyUser(user: api.User): Promise<boolean> {
  if (!user?.userSignature?.armor || !user.userSignature?.fingerprint || !user.serverSignature) {
    console.error('[verifyUser] missing signatures', user?.id);
    return false;
  }

  const armor = await resolvePublicKeyArmor(user.id, user.userSignature.fingerprint);
  if (!armor) {
    console.error('[verifyUser] public key unavailable', user.userSignature.fingerprint);
    return false;
  }

  const userPayload = buildUserIdentityPayload(
    user.username,
    user.userSignature.fingerprint,
    user.avatarURL ?? '',
    user.bio ?? ''
  );
  let userSigArmor: string;
  try {
    userSigArmor = atob(user.userSignature.armor);
  } catch {
    console.error('[verifyUser] invalid userSignature encoding');
    return false;
  }
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyUser] user signature failed', user.id);
    return false;
  }

  const serverPayload = buildProfilePayload(
    user.id,
    user.username,
    user.userSignature.fingerprint,
    user.avatarURL ?? '',
    user.serverSignature.serverID,
    user.serverSignature.fingerprint,
    user.userSignature.armor,
    user.invitedBy?.id ?? '',
    user.bio ?? '',
    signedAtHeader(user.memberSince),
    signedAtHeader(user.serverSignature.timestamp)
  );
  const serverResult = await verify(user.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyUser] server signature failed', serverResult);
    return false;
  }
  return true;
}

export async function verifyReed(reed: ReedType): Promise<boolean> {
  if (
    !reed?.userSignature?.armor ||
    !reed.userSignature?.fingerprint ||
    !reed.userID ||
    !reed.serverSignature
  ) {
    console.error('[verifyReed] missing signatures', reed?.id);
    return false;
  }

  const publicKeyData = await apiService.getPublicKey(reed.userID, reed.userSignature.fingerprint);
  if (!(await verifyPublicKey(publicKeyData))) {
    console.error('[verifyReed] author public key attestation failed', reed.id);
    return false;
  }

  const authorValid = await cryptoService.verifySignature(
    reedAsMarkdown(reed),
    atob(reed.userSignature.armor),
    publicKeyData.armor
  );
  if (!authorValid) {
    console.error('[verifyReed] author signature failed', reed.id);
    return false;
  }

  const serverPayload = buildReedPayload(
    reed.serverSignature.serverID,
    reed.userID,
    reed.id,
    reed.serverSignature.fingerprint,
    reed.userSignature.armor,
    signedAtHeader(reed.serverSignature.timestamp)
  );
  const serverResult = await verify(reed.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyReed] server signature failed', serverResult);
    return false;
  }
  return true;
}

export async function verifyReedRemoval(cert: api.ReedRemoval): Promise<boolean> {
  if (!cert || cert.type !== 'reed' || !cert.userSignature?.armor || !cert.serverSignature) {
    console.error('[verifyReedRemoval] missing fields or wrong type', cert?.type);
    return false;
  }

  const armor = await resolveAuthorArmorForRemoval(cert.userID, cert.userSignature.fingerprint);
  if (!armor) {
    console.error('[verifyReedRemoval] no public key for author', cert.userID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(cert.userSignature.armor);
  } catch {
    console.error('[verifyReedRemoval] invalid signature encoding');
    return false;
  }

  const userPayload = buildReedRemovalUserPayload(cert.serverID, cert.userID, cert.reedID);
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyReedRemoval] user signature failed', cert.reedID);
    return false;
  }

  const serverPayload = buildReedRemovalServerPayload(
    cert.serverID,
    cert.userID,
    cert.reedID,
    cert.serverSignature.fingerprint,
    cert.userSignature.armor,
    signedAtHeader(cert.serverSignature.timestamp)
  );
  const serverResult = await verify(cert.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyReedRemoval] server signature failed', serverResult);
    return false;
  }
  return true;
}

const MAX_ACCOUNT_NOTE_LEN = 140;

export async function verifyAccountRemoval(cert: api.AccountRemoval): Promise<boolean> {
  if (!cert || cert.type !== 'account' || !cert.userSignature?.armor || !cert.serverSignature) {
    console.error('[verifyAccountRemoval] missing fields or wrong type', cert?.type);
    return false;
  }
  if ((cert.note?.length ?? 0) > MAX_ACCOUNT_NOTE_LEN) {
    console.error('[verifyAccountRemoval] note too long');
    return false;
  }

  const armor = await resolveAuthorArmorForRemoval(cert.userID, cert.userSignature.fingerprint);
  if (!armor) {
    console.error('[verifyAccountRemoval] no public key for author', cert.userID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(cert.userSignature.armor);
  } catch {
    console.error('[verifyAccountRemoval] invalid signature encoding');
    return false;
  }

  const userPayload = buildAccountRemovalUserPayload(
    cert.serverID,
    cert.userID,
    cert.note ?? ''
  );
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyAccountRemoval] user signature failed', cert.userID);
    return false;
  }

  const serverPayload = buildAccountRemovalServerPayload(
    cert.serverID,
    cert.userID,
    cert.note ?? '',
    cert.serverSignature.fingerprint,
    cert.userSignature.armor,
    signedAtHeader(cert.serverSignature.timestamp)
  );
  const serverResult = await verify(cert.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyAccountRemoval] server signature failed', serverResult);
    return false;
  }
  return true;
}

async function resolveAuthorArmorForRemoval(
  userID: string,
  fingerprint?: string
): Promise<string | null> {
  try {
    if (fingerprint) {
      const byFp = await resolvePublicKeyArmor(userID, fingerprint);
      if (byFp) return byFp;
    }

    const allKeys = await dbService.getAll<api.PublicKey>('publicKeys');
    const forUser = allKeys.filter((k) => k.userID === userID && k.armor);
    if (forUser.length === 1) return forUser[0].armor;

    const user = await apiService.getUser(userID).catch(() => null);
    const fp =
      fingerprint ||
      user?.activeKeyFingerprint ||
      user?.userSignature?.fingerprint ||
      forUser[0]?.fingerprint;
    if (!fp) return forUser[0]?.armor ?? null;
    return resolvePublicKeyArmor(userID, fp);
  } catch (error) {
    console.error('[resolveAuthorArmorForRemoval] failed', userID, error);
    return null;
  }
}
