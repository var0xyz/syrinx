import { goto } from '$app/navigation';
import { authService } from './auth';
import { isImportComplete, isImportInProgress } from './importRun';
import { isRecoveryInProgress } from './recoveryRun';

/**
 * Send the user to the correct restore step, if any. Returns true when redirected.
 */
export function redirectForRestoreState(): boolean {
  if (authService.isLoggedIn()) {
    goto('/reeds');
    return true;
  }
  if (isImportInProgress()) {
    goto('/import');
    return true;
  }
  if (isImportComplete() && isRecoveryInProgress()) {
    goto('/recovery');
    return true;
  }
  return false;
}
