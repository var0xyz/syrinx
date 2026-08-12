/**
 * Client-side import/recovery gate: mirror server ongoing_recoveries UX
 * using local importRun + recoveryRun markers (see recovery proposal 10/15).
 */

import { authService } from './auth';
import { isImportComplete, isImportInProgress } from './importRun';
import {
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

/**
 * Send the user to the correct restore step, if any. Returns true when redirected.
 * Prefer this on entry surfaces (home, import). Layout uses enforceImportGate
 * for ongoing navigation.
 *
 * Requires a resolvable user record before sending to /reeds — userId alone in
 * localStorage (or a broken/stale SW shell) must not ping-pong / ↔ /reeds.
 */
export async function redirectForRestoreState(): Promise<boolean> {
  if (typeof window !== 'undefined') {
    if (enforceImportGate(window.location.pathname)) {
      return true;
    }
  } else if (isImportInProgress()) {
    navigate('/import');
    return true;
  } else if (isImportGated()) {
    navigate('/recovery');
    return true;
  }

  if (!authService.isLoggedIn()) {
    return false;
  }

  const user = await authService.getCurrentUser();
  if (user) {
    navigate('/reeds');
    return true;
  }

  // TODO: this is the IndexedDB schema-bump wipe (db.ts onupgradeneeded
  // deletes every store, including users/privateKeys) firing on a deploy
  // that changes db.ts's version — localStorage's userId marker survives
  // (separate storage), IndexedDB's user record does not. Net effect: the
  // user is silently signed out and their private key is gone from
  // IndexedDB, with no prompt to re-import. Needs a real decision (auto
  // route to /import, or scope the wipe to only the store that changed)
  // before touching this — see conversation from 2026-08-12.
  console.warn(
    'restoreFlow: local session markers present but no user in IndexedDB; staying put'
  );
  return false;
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
