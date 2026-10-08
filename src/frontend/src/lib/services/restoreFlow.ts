/**
 * Client-side import/recovery gate: mirror server ongoing_recoveries UX
 * using local importRun + recoveryRun markers (see recovery proposal 10/15).
 */

import { authService } from './auth';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { dbService } from './db';
import { clearImportRun, isImportComplete, isImportInProgress } from './importRun';
import {
  clearRecoveryRun,
  isRecoveryComplete,
  isRecoveryInProgress,
  resumeRecoveryRun,
} from './recoveryRun';

function navigate(path: string): void {
  void import('$app/navigation').then(({ goto }) => goto(path));
}

/** Import finished and recovery started but not completed — SPA import-gated. */
export function isImportGated(): boolean {
  return isImportComplete() && isRecoveryInProgress();
}

function pathAllowedDuringImport(pathname: string): boolean {
  return pathname === '/import' || pathname.startsWith('/import/');
}

function pathAllowedDuringRecovery(pathname: string): boolean {
  return (
    pathname === '/recovery' ||
    pathname === '/recover' ||
    pathname.startsWith('/recovery/')
  );
}

/**
 * Target path when the current URL is blocked by mid-import or import-gate,
 * or null if the path is allowed.
 */
export function importGateRedirect(pathname: string): string | null {
  if (isImportInProgress()) {
    return pathAllowedDuringImport(pathname) ? null : '/import';
  }
  if (isImportGated()) {
    return pathAllowedDuringRecovery(pathname) ? null : '/recovery';
  }
  return null;
}

/**
 * Redirect when mid-import or import-gated. Returns true if a redirect was
 * initiated.
 */
export function enforceImportGate(pathname: string): boolean {
  const dest = importGateRedirect(pathname);
  if (!dest) return false;
  navigate(dest);
  return true;
}

/** Where the user belongs now: a restore step, their profile (/reeds), or
 * null to stay. Never navigates; clears a session it can't sign with, so
 * / and /reeds can't bounce off each other. */
export async function resolveRestoreTarget(): Promise<string | null> {
  if (typeof window !== 'undefined') {
    const dest = importGateRedirect(window.location.pathname);
    if (dest) return dest;
  } else if (isImportInProgress()) {
    return '/import';
  } else if (isImportGated()) {
    return '/recovery';
  }

  if (!authService.isLoggedIn()) {
    return null;
  }

  // <Auth> sends a session it can't sign with back to /, so only a user
  // record together with its active private key may go on to /reeds.
  const user = await authService.getCurrentUser();
  const keyId = authService.getActiveKeyId();
  const keyHeld = !!keyId && (await privateKeyRepository.hasPrivateKey(keyId));
  if (user && keyHeld) {
    return '/reeds';
  }

  // userId survived (localStorage) but IndexedDB has no matching record or
  // key — an IndexedDB wipe (schema bump) or a stale importRun marker left
  // the session unusable. Clear it and send the user to re-establish identity.
  console.warn(
    `restoreFlow: session markers present but ${user ? 'no private key for ' + keyId : 'no user'} in IndexedDB; clearing and sending to /import`
  );
  localStorage.removeItem('userId');
  clearImportRun();
  return '/import';
}

/**
 * Send the user to the correct restore step, if any. Returns true when redirected.
 * Prefer this on entry surfaces (welcome, import). Layout uses enforceImportGate
 * for ongoing navigation.
 */
export async function redirectForRestoreState(): Promise<boolean> {
  const target = await resolveRestoreTarget();
  if (!target) return false;
  navigate(target);
  return true;
}

// Set up before any import (server trust, this browser's device id).
const KEPT_ON_DISCARD = ['deviceId', 'serverKeyArmor', 'serverKeyFingerprint'];

/**
 * Undo a failed, cancelled import: drop its markers and everything it may
 * have written, so the device is back to having no identity.
 */
export async function discardFailedImport(): Promise<void> {
  clearImportRun();
  clearRecoveryRun();
  for (const key of Object.keys(localStorage)) {
    if (!KEPT_ON_DISCARD.includes(key)) localStorage.removeItem(key);
  }
  await authService.clearSession();
  await dbService.deleteDatabase();
}

const FINISH_RECOVERY_RE = /finish recovery/i;

export function isFinishRecoveryForbiddenMessage(message: string): boolean {
  return FINISH_RECOVERY_RE.test(message);
}

export function isDeviceMismatchError(message: string): boolean {
  return /device mismatch/i.test(message);
}

/**
 * Device binding rejected this browser — clear session identity only (not IndexedDB).
 */
export function handleDeviceMismatch(): void {
  if (typeof window === 'undefined') return;
  console.warn("Device mismatch, logging user out")
  localStorage.removeItem('userId');
  void authService.clearSession();
}

/**
 * Optional API 403 fallback: server still has ongoing_recoveries. Ensure a
 * local recovery run and send the user to the recovery UI.
 */
export function handleFinishRecoveryForbidden(): void {
  if (typeof window === 'undefined') return;

  if (isImportComplete() && !isRecoveryInProgress() && !isRecoveryComplete()) {
    resumeRecoveryRun();
  }

  if (!isRecoveryInProgress() && !isImportComplete()) {
    // No local restore context to resume — leave caller to surface the error.
    return;
  }

  if (!pathAllowedDuringRecovery(window.location.pathname)) {
    navigate('/recovery');
  }
}
