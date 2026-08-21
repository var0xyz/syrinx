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
import { serverConnection } from '$lib/services/serverConnection';
import { reedContentWithinLimits } from '$lib/utils/reedContent';
import { shouldRecheck, markChecked } from '$lib/utils/keyCheckThrottle';
import { parseKeyId } from '$lib/utils/keyId';
import { canonicalKeyId } from '$lib/services/api';
import {
  buildAccountRemovalServerPayload,
  buildAccountRemovalUserPayload,
  buildInviteServerPayload,
  buildInviteUserPayload,
  buildProfilePayload,
  buildPublicKeyPayload,
  buildReedPayload,
  buildReedLikeServerPayload,
  buildReedLikeUserPayload,
  buildReedRemovalServerPayload,
  buildReedRemovalUserPayload,
  buildRippleServerPayload,
  buildRippleUserPayload,
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

/** Fetches, attests, and stores fresh key data. Reports genuine fetch
 * failures (anything but "key not found") since content already arrived
 * over an authenticated connection — the server was reachable. */
async function fetchAndStorePublicKey(
  userID: string,
  fingerprint: string
): Promise<api.PublicKey | null> {
  try {
    const key = await apiService.getPublicKey(canonicalKeyId(userID, fingerprint));
    if (!key) return null;
    await dbService.put('publicKeys', key, verifyPublicKey);
    markChecked(fingerprint);
    return key;
  } catch (err) {
    const status = (err as { status?: number })?.status;
    if (status !== 404) {
      serverConnection.sendKeyFetchError(userID, fingerprint);
    }
    return null;
  }
}

async function resolvePublicKeyArmor(
  userID: string,
  fingerprint: string
): Promise<string | null> {
  const cached = await dbService.get<api.PublicKey>('publicKeys', fingerprint);
  if (cached?.armor && !shouldRecheck(fingerprint)) return cached.armor;
  const key = await fetchAndStorePublicKey(userID, fingerprint);
  return key?.armor ?? null;
}

/** Cached public key, re-fetching + attesting + storing on miss or once the
 * sessionStorage throttle window has elapsed since the last check. A failed
 * re-check (transient network error, not 404) fails closed rather than
 * falling back to the stale cached copy — freshness could not be confirmed. */
async function resolvePublicKey(
  userID: string,
  fingerprint: string
): Promise<api.PublicKey | null> {
  const cached = await dbService.get<api.PublicKey>('publicKeys', fingerprint);
  if (cached && !shouldRecheck(fingerprint)) return cached;
  return await fetchAndStorePublicKey(userID, fingerprint);
}

async function resolvePredecessor(key: api.PublicKey): Promise<api.PublicKey | null> {
  const predId = key.predecessor?.id;
  if (!predId) return null;
  const cached = await dbService.get<api.PublicKey>('publicKeys', predId);
  if (cached) return cached;
  try {
    const pred = await apiService.getPublicKey(canonicalKeyId(key.userID, predId));
    if (!pred) return null;
    try {
      await dbService.put('publicKeys', pred, verifyPublicKey);
    } catch {
      // Still hand the fetched key to the current attestation pass.
    }
    return pred;
  } catch {
    return null;
  }
}

/** Fetch + cache the predecessor key's own revocation cert, needed for its
 * `successorSignature` (the predecessor's proof it approved this rotation —
 * see verifyPublicKey's predecessor handoff check). */
async function resolvePredecessorRevocation(predId: string): Promise<api.KeyRevocation | null> {
  const cached = await dbService.get<api.KeyRevocation>('revocations', predId);
  if (cached) return cached;
  if (!parseKeyId(predId)) return null;
  try {
    // predId is already canonical, so getKeyRevocation's userId arg is
    // moot (canonicalKeyId in api.ts passes an already-canonical
    // fingerprint straight through) — pass it again rather than
    // re-deriving userId@serverID from the parsed id.
    const revocation = await apiService.getKeyRevocation(predId, predId);
    try {
      await dbService.put('revocations', revocation, verifyKeyRevocation);
    } catch {
      // Still hand the fetched revocation to the current attestation pass.
    }
    return revocation;
  } catch {
    return null;
  }
}

/** Server attestation + armor↔id (+ optional predecessor handoff). */
export async function verifyPublicKey(key: api.PublicKey): Promise<boolean> {
  if (!key?.serverSignature) {
    console.error('[verifyPublicKey] missing serverSignature block', key?.id);
    return false;
  }
  const result = await verify(
    key.serverSignature,
    buildPublicKeyPayload(
      key.userID,
      key.id,
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
  // key.id is canonical (userID@serverID/fingerprint); the armor only lets
  // us derive the bare fingerprint, so compare against the parsed suffix.
  // Also cross-check the embedded userID matches key.userID (mirrors the
  // equivalent check added server-side in recovery/nest.go's
  // FlattenKeysNest) — closes a spoofing gap where a tampered id string
  // could claim a different owner than the one the rest of the record
  // asserts.
  const parsedId = parseKeyId(key.id);
  if (!parsedId) {
    console.error('[verifyPublicKey] malformed id', key.id);
    return false;
  }
  const embeddedUserId = `${parsedId.userId}@${parsedId.serverId}`;
  if (embeddedUserId !== key.userID) {
    console.error('[verifyPublicKey] id owner mismatch', { id: key.id, userID: key.userID });
    return false;
  }
  const derived = await cryptoService.fingerprintFromArmor(key.armor);
  if (derived.toLowerCase() !== parsedId.fingerprint.toLowerCase()) {
    console.error('[verifyPublicKey] fingerprint mismatch', { labeled: key.id, derived });
    return false;
  }

  // Rotation provenance is one hop away now: the predecessor no longer
  // carries its own handoff signature (KeyPredecessor is just {id}) — that
  // proof lives on the *predecessor's own* revocation cert, as
  // successorSignature (see types/api.ts's KeyPredecessor doc comment).
  if (key.predecessor?.id) {
    const predecessor = await resolvePredecessor(key);
    if (!predecessor?.armor) {
      console.error('[verifyPublicKey] predecessor public key unavailable', key.predecessor.id);
      return false;
    }
    const predRevocation = await resolvePredecessorRevocation(key.predecessor.id);
    if (!predRevocation?.successorSignature) {
      console.error('[verifyPublicKey] predecessor revocation/successorSignature unavailable', key.predecessor.id);
      return false;
    }
    if (predRevocation.successor !== key.id) {
      console.error('[verifyPublicKey] predecessor revocation successor mismatch', {
        expected: key.id,
        actual: predRevocation.successor,
      });
      return false;
    }
    // successorSignature travels as raw armor, not base64 — it's written
    // from the same already-decoded value the old predecessor.signature
    // field used to carry (see AddPublicKeyInput.PredecessorSignature's
    // doc comment in services.go), unlike userSignature.armor/
    // serverSignature.armor elsewhere on this cert, which are base64.
    const handoffValid = await cryptoService.verifySignature(
      key.armor,
      predRevocation.successorSignature,
      predecessor.armor
    );
    if (!handoffValid) {
      console.error('[verifyPublicKey] predecessor handoff signature failed', key.id);
      return false;
    }
  }

  return true;
}

/** True if a key was valid at the given (server-attested) instant: not
 * revoked, or revoked strictly after that instant — a key remains valid for
 * content it signed before its own revocation. Fails closed (false) if the
 * revocation record can't be fetched, and reports REVOKED_KEY_USED when
 * content was genuinely signed at/after revocation. */
async function isKeyValidAt(
  userID: string,
  fingerprint: string,
  revoked: boolean,
  atISO: string
): Promise<boolean> {
  if (!revoked) return true;
  let revocation: api.KeyRevocation;
  try {
    revocation = await apiService.getKeyRevocation(userID, fingerprint);
  } catch {
    return false;
  }
  if (!revocation?.serverSignature?.timestamp) return false;
  const valid = Date.parse(atISO) < Date.parse(revocation.serverSignature.timestamp);
  if (!valid) {
    serverConnection.sendRevokedKeyUsed(userID, fingerprint);
  }
  return valid;
}

export async function verifyKeyRevocation(revocation: api.KeyRevocation): Promise<boolean> {
  if (!revocation?.serverSignature || !revocation.userSignature?.armor) {
    console.error('[verifyKeyRevocation] missing signatures', revocation?.id);
    return false;
  }

  // User attestation is signed by the key being revoked.
  const publicKeyArmor = await resolvePublicKeyArmor(revocation.userID, revocation.id);
  if (!publicKeyArmor) {
    console.error('[verifyKeyRevocation] public key armor unavailable', revocation.id);
    return false;
  }

  const userPayload = buildUserRevocationPayload(
    revocation.userID,
    revocation.id,
    revocation.reason
  );
  const userValid = await cryptoService.verifySignature(
    userPayload,
    atob(revocation.userSignature.armor),
    publicKeyArmor
  );
  if (!userValid) {
    console.error('[verifyKeyRevocation] user signature failed', revocation.id);
    return false;
  }

  const serverPayload = buildServerRevocationPayload(
    revocation.userID,
    revocation.id,
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
  if (user.role !== 'root' && user.role !== 'admin' && user.role !== 'user') {
    console.error('[verifyUser] missing or invalid role', user?.id, user?.role);
    return false;
  }

  const publicKeyData = await resolvePublicKey(user.id, user.userSignature.fingerprint);
  if (!publicKeyData?.armor) {
    console.error('[verifyUser] public key unavailable', user.userSignature.fingerprint);
    return false;
  }

  const userPayload = buildUserIdentityPayload(
    user.username,
    user.userSignature.fingerprint,
    user.bio ?? ''
  );
  let userSigArmor: string;
  try {
    userSigArmor = atob(user.userSignature.armor);
  } catch {
    console.error('[verifyUser] invalid userSignature encoding');
    return false;
  }
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, publicKeyData.armor);
  if (!userValid) {
    console.error('[verifyUser] user signature failed', user.id);
    return false;
  }

  const serverPayload = buildProfilePayload(
    user.id,
    user.username,
    user.userSignature.fingerprint,
    user.serverSignature.serverID,
    user.serverSignature.fingerprint,
    user.userSignature.armor,
    user.invitedBy?.id ?? '',
    user.role,
    user.bio ?? '',
    signedAtHeader(user.memberSince),
    signedAtHeader(user.serverSignature.timestamp)
  );
  const serverResult = await verify(user.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyUser] server signature failed', serverResult);
    return false;
  }

  if (
    !(await isKeyValidAt(
      user.id,
      user.userSignature.fingerprint,
      publicKeyData.revoked,
      user.serverSignature.timestamp
    ))
  ) {
    console.error('[verifyUser] key was revoked before this profile update was signed', user.id);
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

  // Persist path only (broadcast is ephemeral and never hits IndexedDB put).
  if (!reedContentWithinLimits(reed.content)) {
    console.error(
      '[verifyReed] content exceeds size limits',
      reed.id,
      'raw=',
      reed.content?.length ?? 0
    );
    return false;
  }

  const publicKeyData = await resolvePublicKey(reed.userID, reed.userSignature.fingerprint);
  if (!publicKeyData) {
    console.error('[verifyReed] author public key unavailable', reed.id);
    return false;
  }
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

  if (
    !(await isKeyValidAt(
      reed.userID,
      reed.userSignature.fingerprint,
      publicKeyData.revoked,
      reed.serverSignature.timestamp
    ))
  ) {
    console.error('[verifyReed] author key was revoked before this reed was signed', reed.id);
    return false;
  }

  return true;
}

/**
 * Verify a ripple response: author signature + server countersignature,
 * modeled on verifyReed. `reedID` (canonical) identifies the parent reed
 * this ripple is attached to — not part of the wire object itself, so the
 * caller (which already knows which reed's list it fetched) supplies it.
 * A tombstoned (soft-deleted) response is trusted on its `deleted` flag
 * alone — its stored signatures describe the original pre-delete content,
 * which this client does not have and cannot re-verify against (see
 * specs/ripples/00_design.md's Client-side verification section).
 */
export async function verifyRipple(
  ripple: api.Ripple,
  reedID: string
): Promise<boolean> {
  if (ripple?.deleted) {
    return true;
  }

  if (
    !ripple?.userSignature?.armor ||
    !ripple.userSignature?.fingerprint ||
    !ripple.userID ||
    !ripple.serverSignature
  ) {
    console.error('[verifyRipple] missing signatures', ripple?.hash);
    return false;
  }

  const publicKeyData = await resolvePublicKey(ripple.userID, ripple.userSignature.fingerprint);
  if (!publicKeyData) {
    console.error('[verifyRipple] author public key unavailable', ripple.hash);
    return false;
  }
  if (!(await verifyPublicKey(publicKeyData))) {
    console.error('[verifyRipple] author public key attestation failed', ripple.hash);
    return false;
  }

  const userPayload = buildRippleUserPayload(
    reedID,
    ripple.userID,
    ripple.userSignature.fingerprint,
    ripple.threadID,
    ripple.replyingTo ?? '',
    ripple.content
  );
  const authorValid = await cryptoService.verifySignature(
    userPayload,
    atob(ripple.userSignature.armor),
    publicKeyData.armor
  );
  if (!authorValid) {
    console.error('[verifyRipple] author signature failed', ripple.hash);
    return false;
  }

  const serverPayload = buildRippleServerPayload(
    ripple.serverSignature.serverID,
    reedID,
    ripple.userID,
    ripple.userSignature.fingerprint,
    ripple.threadID,
    ripple.replyingTo ?? '',
    ripple.userSignature.armor,
    signedAtHeader(ripple.serverSignature.timestamp)
  );
  const serverResult = await verify(ripple.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyRipple] server signature failed', serverResult);
    return false;
  }

  if (
    !(await isKeyValidAt(
      ripple.userID,
      ripple.userSignature.fingerprint,
      publicKeyData.revoked,
      ripple.serverSignature.timestamp
    ))
  ) {
    console.error('[verifyRipple] author key was revoked before this ripple was signed', ripple.hash);
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

/** A like is always the signed-in user's own — resolve the liker's key
 * against the local userID rather than any wire-carried identity. */
export async function verifyReedLike(cert: api.ReedLike): Promise<boolean> {
  if (!cert || !cert.userSignature?.armor || !cert.serverSignature) {
    console.error('[verifyReedLike] missing fields');
    return false;
  }

  const likerID = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  if (!likerID) {
    console.error('[verifyReedLike] no signed-in user');
    return false;
  }

  const armor = await resolvePublicKeyArmor(likerID, cert.userSignature.fingerprint);
  if (!armor) {
    console.error('[verifyReedLike] no public key for liker', likerID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(cert.userSignature.armor);
  } catch {
    console.error('[verifyReedLike] invalid signature encoding');
    return false;
  }

  const userPayload = buildReedLikeUserPayload(
    cert.serverID,
    cert.authorID,
    cert.reedID,
    cert.userSignature.fingerprint
  );
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyReedLike] user signature failed', cert.reedID);
    return false;
  }

  const serverPayload = buildReedLikeServerPayload(
    cert.serverID,
    cert.authorID,
    cert.reedID,
    cert.serverSignature.fingerprint,
    cert.userSignature.armor,
    signedAtHeader(cert.serverSignature.timestamp)
  );
  const serverResult = await verify(cert.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyReedLike] server signature failed', serverResult);
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

export async function verifyInvite(invite: api.Invite): Promise<boolean> {
  if (
    !invite?.id ||
    !invite.createdAt ||
    !invite.userSignature?.armor ||
    !invite.userSignature?.fingerprint ||
    !invite.serverSignature
  ) {
    console.error('[verifyInvite] missing fields', invite?.id);
    return false;
  }

  const userID = localStorage.getItem('userId');
  if (!userID) {
    console.error('[verifyInvite] no local userId');
    return false;
  }

  const armor = await resolvePublicKeyArmor(userID, invite.userSignature.fingerprint);
  if (!armor) {
    console.error('[verifyInvite] no public key', userID);
    return false;
  }

  let userSigArmor: string;
  try {
    userSigArmor = atob(invite.userSignature.armor);
  } catch {
    console.error('[verifyInvite] invalid userSignature encoding');
    return false;
  }

  const createdAt = signedAtHeader(invite.createdAt);
  if (!invite.tokenHash) {
    console.error('[verifyInvite] missing tokenHash', invite.id);
    return false;
  }
  const userPayload = buildInviteUserPayload(
    invite.serverSignature.serverID,
    userID,
    invite.id,
    invite.tokenHash,
    createdAt,
    invite.grantedRole ?? 'user'
  );
  const userValid = await cryptoService.verifySignature(userPayload, userSigArmor, armor);
  if (!userValid) {
    console.error('[verifyInvite] user signature failed', invite.id);
    return false;
  }

  const serverPayload = buildInviteServerPayload(
    invite.serverSignature.serverID,
    userID,
    invite.id,
    invite.tokenHash,
    invite.serverSignature.fingerprint,
    invite.userSignature.armor,
    createdAt,
    signedAtHeader(invite.serverSignature.timestamp)
  );
  const serverResult = await verify(invite.serverSignature, serverPayload);
  if (serverResult.ok === false) {
    console.error('[verifyInvite] server signature failed', serverResult);
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

    const info = await apiService.getUserInfo(userID).catch(() => null);
    const fp =
      fingerprint ||
      info?.activeKeyFingerprint ||
      forUser[0]?.id;
    if (!fp) return forUser[0]?.armor ?? null;
    return resolvePublicKeyArmor(userID, fp);
  } catch (error) {
    console.error('[resolveAuthorArmorForRemoval] failed', userID, error);
    return null;
  }
}
