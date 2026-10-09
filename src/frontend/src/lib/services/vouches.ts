import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildVouchUserPayload, buildVouchWithdrawalUserPayload } from './signing';
import { vouchesRepository, type VouchRecord } from '$lib/repositories/vouches';
import { trustRootsRepository } from '$lib/repositories/trustRoots';
import { userInfoRepository } from '$lib/repositories/userInfo';
import { pendingVouchesRepository } from '$lib/repositories/pendingVouches';
import { declinedVouchesRepository } from '$lib/repositories/declinedVouches';
import { verifyVouch } from '$lib/verifiers';
import { MAX_VOUCH_NOTE_CHARS } from '$lib/utils/vouchNote';
import { countsForMark, trustMarkFrom, type TrustMark } from '$lib/utils/trustMark';
import { findContradiction } from '$lib/utils/vouchContradiction';
import { classifyKeyChange, type KeyChangeKind } from '$lib/utils/keyChange';
import { dbService } from './db';
import { foreignServerOf } from './peerServers';

export type { TrustMark, KeyChangeKind };

/** The signed-in user, or null before sign-in and during SSR. */
function me(): string | null {
  return typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
}

/**
 * Reconciles the server's id list against what this client already
 * verified, fetching and verifying only the ids it has never seen.
 * Returns false when anything failed, so callers can hold off on marks.
 */
export async function reconcileVouches(
  subjectUserID: string,
  serverVouchIDs: string[]
): Promise<boolean> {
  let complete = true;
  const seen = new Set(serverVouchIDs);
  const held = await vouchesRepository.forSubject(subjectUserID);

  // Fetched concurrently so the display converges as answers arrive
  // rather than after the slowest round trip.
  const unknown = serverVouchIDs.filter((id) => !held.some((v) => v.id === id));
  await Promise.all(
    unknown.map(async (vouchID) => {
      try {
        const cert = await apiService.getVouch(subjectUserID, vouchID);
        // put verifies; a cert that fails is not stored and not counted.
        await vouchesRepository.put(cert, subjectUserID);
      } catch (error) {
        console.error('[reconcileVouches] refused', vouchID, error);
        complete = false;
      }
    })
  );

  // An id that vanished was withdrawn: fetch it once for the signed
  // retraction, then drop it. A proven withdrawal is not worth keeping.
  await Promise.all(
    held
      .filter((vouch) => !seen.has(vouch.id))
      .map(async (vouch) => {
        try {
          const cert = await apiService.getVouch(subjectUserID, vouch.id);
          if (cert.withdrawal) {
            await vouchesRepository.put(cert, subjectUserID);
            await vouchesRepository.delete(vouch.id, me());
          }
        } catch (error) {
          console.error('[reconcileVouches] could not confirm withdrawal', vouch.id, error);
          complete = false;
        }
      })
  );

  return complete;
}

/**
 * The mark for a subject, computed from local data only. Call after
 * reconcileVouches has returned true — an unreconciled profile shows no
 * mark rather than a provisional one.
 */
export async function trustMarkFor(
  subjectUserID: string,
  activeKeyID: string
): Promise<TrustMark> {
  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  const vouches = await vouchesRepository.forSubject(subjectUserID);
  const roots = await trustRootsRepository.activeIDs();
  return trustMarkFrom(vouches, activeKeyID, me, roots);
}

const refreshing = new Set<string>();

/**
 * Fetches the subject's current key and vouch ids, then reconciles. The
 * mark is drawn from local data first; this corrects it in the background.
 */
export async function refreshVouches(subjectUserID: string): Promise<void> {
  if (!subjectUserID || refreshing.has(subjectUserID)) return;
  refreshing.add(subjectUserID);
  try {
    const info = await apiService.getUserInfo(subjectUserID);
    await userInfoRepository.put(info);
    await reconcileVouches(subjectUserID, info.vouchIds ?? []);
  } catch (error) {
    console.error('[vouches] background refresh failed', subjectUserID, error);
  } finally {
    refreshing.delete(subjectUserID);
  }
}

/** Verified vouches naming the subject's current key, for the profile. */
export async function liveVouchesFor(
  subjectUserID: string,
  activeKeyID: string
): Promise<VouchRecord[]> {
  const vouches = await vouchesRepository.forSubject(subjectUserID);
  return vouches.filter((v) => countsForMark(v, activeKeyID));
}

/** Vouches on a key the subject has since replaced — a weaker signal. */
export async function staleVouchesFor(
  subjectUserID: string,
  activeKeyID: string
): Promise<VouchRecord[]> {
  const vouches = await vouchesRepository.forSubject(subjectUserID);
  return vouches.filter((v) => !v.withdrawal && v.subjectKeyId !== activeKeyID);
}

/** Stores a vouch the server pushed by id. The cert is fetched and verified
 * here, so a push can only draw attention to one, never assert it. */
export async function ingestPushedVouch(vouchID: string): Promise<boolean> {
  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  if (!me || !vouchID) return false;
  if (await vouchesRepository.has(vouchID)) return true;
  try {
    const cert = await apiService.getVouch(me, vouchID);
    await vouchesRepository.put(cert, me);
    return true;
  } catch (error) {
    console.error('[vouches] could not ingest pushed vouch', vouchID, error);
    return false;
  }
}

/**
 * Classifies how the subject's current key relates to a key this device
 * vouched for. Null when nothing was vouched or the key still matches.
 */
export async function keyChangeFor(
  subjectUserID: string,
  currentKeyID: string
): Promise<KeyChangeKind | null> {
  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  if (!me) return null;
  const mine = (await vouchesRepository.forSubject(subjectUserID)).find(
    (v) => v.voucherUserId === me
  );
  if (!mine) return null;

  const current = await dbService.get<api.PublicKey>('publicKeys', currentKeyID);
  const vouched = await dbService.get<api.PublicKey>('publicKeys', mine.subjectKeyId);
  return classifyKeyChange({
    vouchedKeyID: mine.subjectKeyId,
    currentKeyID,
    predecessorID: current?.predecessor ?? null,
    // verifyPublicKey refuses a key whose handoff does not check out, so a
    // stored key that declares a predecessor carries a valid one.
    handoffValid: !!current,
    vouchedKeyRevoked: !!vouched?.revoked,
  });
}

/** A live vouch naming a different key for this user, if one exists — a
 * contradiction of what the server reports, not merely an absence. */
export async function contradictingVouch(
  subjectUserID: string,
  claimedKeyID: string
): Promise<VouchRecord | null> {
  const vouches = await vouchesRepository.forSubject(subjectUserID);
  return findContradiction(vouches, claimedKeyID);
}

/**
 * Signs and posts a vouch, then adds the subject to local trust roots.
 * subjectKeyID is the key that was actually compared out of band, not
 * whatever the server currently reports.
 */
export async function createVouch(
  subjectUserID: string,
  subjectKeyID: string,
  note = ''
): Promise<api.Vouch> {
  if (note.length > MAX_VOUCH_NOTE_CHARS) {
    throw new Error(`Note cannot exceed ${MAX_VOUCH_NOTE_CHARS} characters`);
  }
  const voucherKeyID = authService.getActiveKeyId();
  if (!voucherKeyID) throw new Error('Active key not available');

  const payload = buildVouchUserPayload(voucherKeyID, subjectKeyID, note);
  const signature = await requestSigner.sign(payload);

  // Queue first: verification happens in person, often with no signal, and
  // the signature must outlive the moment rather than the meeting repeating.
  // A user of another server is the exception: their server must accept it
  // now, so a failure is reported to retry instead of queued.
  const foreign = foreignServerOf(subjectUserID) !== null;
  if (!foreign) {
    await pendingVouchesRepository.put({
      compositeKey: subjectKeyID,
      subjectUserID,
      subjectKeyID,
      voucherKeyID,
      note,
      signature,
    });
  }

  const cert = await apiService.createVouch(subjectKeyID, voucherKeyID, signature, note);
  await vouchesRepository.put(cert, subjectUserID);
  await trustRootsRepository.add(subjectUserID, subjectKeyID);
  if (!foreign) await pendingVouchesRepository.delete(subjectKeyID);
  return cert;
}

/** Every vouch the caller made, newest first. Local, because only a
 * complete record answers "did I do all of these?". */
export async function myVouches(): Promise<api.Vouch[]> {
  const id = me();
  if (!id) return [];
  return vouchesRepository.byVoucher(id);
}

/**
 * Verified vouches the server holds under the caller's name that this
 * device has neither accepted nor declined. Throws if the server can't be read.
 */
export async function unreviewedVouches(): Promise<api.Vouch[]> {
  const id = me();
  if (!id) return [];

  const declined = new Set((await declinedVouchesRepository.byVoucher(id)).map((v) => v.id));
  const found: api.Vouch[] = [];
  let cursor: string | undefined;
  do {
    const page = await apiService.getMyVouches(cursor);
    for (const cert of page.vouches ?? []) {
      if (cert.voucherUserId !== id || declined.has(cert.id)) continue;
      if (await vouchesRepository.has(cert.id)) continue;
      if (await verifyVouch(cert, cert.subjectUserId)) found.push(cert);
    }
    cursor = page.nextCursor;
  } while (cursor);
  return found;
}

/** Adds a server-held vouch to the caller's own list, undoing a decline. */
export async function acceptVouch(cert: api.Vouch): Promise<void> {
  await vouchesRepository.put(cert, cert.subjectUserId);
  await declinedVouchesRepository.delete(cert.id);
}

/** Records that the caller doesn't recognise a vouch the server holds. */
export async function declineVouch(cert: api.Vouch): Promise<void> {
  await declinedVouchesRepository.put(cert);
}

export async function myDeclinedVouches(): Promise<api.Vouch[]> {
  const id = me();
  if (!id) return [];
  return declinedVouchesRepository.byVoucher(id);
}

/** What the audit list shows for one vouch the caller made. */
export type AuditState = 'withdrawn' | 'revoked key' | 'previous key' | 'live';

/**
 * Computed locally, never reported by the server. A vouch stops applying
 * once the key it names is revoked or replaced.
 */
export async function auditStateFor(vouch: api.Vouch): Promise<AuditState> {
  if (vouch.withdrawal) return 'withdrawn';

  // Only set for keys this device revoked itself, so it is a bonus signal
  // rather than the check: the key comparison below is what catches the rest.
  const vouched = await dbService.get<api.PublicKey>('publicKeys', vouch.subjectKeyId);
  if (vouched?.revoked) return 'revoked key';

  const currentKeyID = await currentKeyIDFor(vouch.subjectUserId);
  if (!currentKeyID) return 'live';
  return vouch.subjectKeyId === currentKeyID ? 'live' : 'previous key';
}

/** Refetched, not cached: a rotation is what this audit must notice.
 * Falls back to the cache offline. */
async function currentKeyIDFor(subjectUserID: string): Promise<string | null> {
  try {
    const info = await apiService.getUserInfo(subjectUserID);
    await userInfoRepository.put(info);
    return info.activeKeyId ?? null;
  } catch (error) {
    console.error('[vouches] could not resolve current key', subjectUserID, error);
    const cached = await userInfoRepository.get(subjectUserID);
    return cached?.activeKeyId ?? null;
  }
}

/**
 * Withdraws a vouch, signed with whatever key is current now — a voucher
 * who rotated must still be able to retract.
 */
export async function withdrawVouch(
  vouchID: string,
  subjectUserID: string,
  subjectKeyID: string
): Promise<api.Vouch> {
  const voucherKeyID = authService.getActiveKeyId();
  if (!voucherKeyID) throw new Error('Active key not available');

  const payload = buildVouchWithdrawalUserPayload(vouchID);
  const signature = await requestSigner.sign(payload);

  const cert = await apiService.withdrawVouch(subjectKeyID, voucherKeyID, signature);
  // put verifies both withdrawal signatures and throws on failure. The
  // record stays: it is the caller's own, and the audit list shows what
  // they retracted rather than hiding it.
  await vouchesRepository.put(cert, subjectUserID);
  await trustRootsRepository.remove(subjectUserID);
  return cert;
}
