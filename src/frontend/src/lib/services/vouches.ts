import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildVouchUserPayload, buildVouchWithdrawalUserPayload } from './signing';
import { vouchesRepository, type VouchRecord } from '$lib/repositories/vouches';
import { trustRootsRepository } from '$lib/repositories/trustRoots';
import { pendingVouchesRepository } from '$lib/repositories/pendingVouches';
import { MAX_VOUCH_NOTE_CHARS } from '$lib/utils/vouchNote';
import { countsForMark, trustMarkFrom, type TrustMark } from '$lib/utils/trustMark';
import { findContradiction } from '$lib/utils/vouchContradiction';

export type { TrustMark };

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

  for (const vouchID of serverVouchIDs) {
    if (await vouchesRepository.has(vouchID)) continue;
    try {
      const cert = await apiService.getVouch(subjectUserID, vouchID);
      // put verifies; a cert that fails is not stored and not counted.
      await vouchesRepository.put(cert);
    } catch (error) {
      console.error('[reconcileVouches] refused', vouchID, error);
      complete = false;
    }
  }

  // An id that vanished from the list is fetched once for its signed
  // withdrawal: a retraction must be proven, not inferred from omission.
  const held = await vouchesRepository.forSubject(subjectUserID);
  for (const vouch of held) {
    if (seen.has(vouch.id) || vouch.withdrawn) continue;
    try {
      const cert = await apiService.getVouch(subjectUserID, vouch.id);
      await vouchesRepository.put(cert);
    } catch (error) {
      console.error('[reconcileVouches] could not confirm withdrawal', vouch.id, error);
      complete = false;
    }
  }

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
  return vouches.filter((v) => !v.withdrawn && v.subjectKeyID !== activeKeyID);
}

/** Stores a vouch the server pushed by id. The cert is fetched and verified
 * here, so a push can only draw attention to one, never assert it. */
export async function ingestPushedVouch(vouchID: string): Promise<boolean> {
  const me = typeof localStorage !== 'undefined' ? localStorage.getItem('userId') : null;
  if (!me || !vouchID) return false;
  if (await vouchesRepository.has(vouchID)) return true;
  try {
    const cert = await apiService.getVouch(me, vouchID);
    await vouchesRepository.put(cert);
    return true;
  } catch (error) {
    console.error('[vouches] could not ingest pushed vouch', vouchID, error);
    return false;
  }
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

  const payload = buildVouchUserPayload(voucherKeyID, subjectUserID, subjectKeyID, note);
  const signature = await requestSigner.sign(payload);

  // Queue first: verification happens in person, often with no signal, and
  // the signature must outlive the moment rather than the meeting repeating.
  await pendingVouchesRepository.put({
    compositeKey: subjectKeyID,
    subjectUserID,
    subjectKeyID,
    voucherKeyID,
    note,
    signature,
  });

  const cert = await apiService.createVouch(
    subjectUserID,
    subjectKeyID,
    voucherKeyID,
    signature,
    note
  );
  await vouchesRepository.put(cert);
  await trustRootsRepository.add(subjectUserID, subjectKeyID);
  await pendingVouchesRepository.delete(subjectKeyID);
  return cert;
}

/**
 * Withdraws a vouch, signed with whatever key is current now — a voucher
 * who rotated must still be able to retract.
 */
export async function withdrawVouch(
  subjectUserID: string,
  subjectKeyID: string
): Promise<api.Vouch> {
  const voucherKeyID = authService.getActiveKeyId();
  if (!voucherKeyID) throw new Error('Active key not available');

  const payload = buildVouchWithdrawalUserPayload(voucherKeyID, subjectUserID, subjectKeyID);
  const signature = await requestSigner.sign(payload);

  const cert = await apiService.withdrawVouch(subjectKeyID, voucherKeyID, signature);
  await vouchesRepository.put(cert);
  return cert;
}
