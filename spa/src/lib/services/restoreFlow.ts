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
 */
export function redirectForRestoreState(): boolean {
  if (authService.isLoggedIn()) {
    navigate('/reeds');
    return true;
  }
  if (typeof window !== 'undefined') {
    return enforceImportGate(window.location.pathname);
  }
  if (isImportInProgress()) {
    navigate('/import');
    return true;
  }
  if (isImportGated()) {
    navigate('/recovery');
    return true;
  }
  return false;
}

const FINISH_RECOVERY_RE = /finish recovery/i;

export function isFinishRecoveryForbiddenMessage(message: string): boolean {
  return FINISH_RECOVERY_RE.test(message);
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
