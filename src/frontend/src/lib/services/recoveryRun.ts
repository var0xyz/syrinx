/**
 * Local recovery-run marker. Progress ledger lives in recoveryProgress.ts.
 */

import {
  clearRecoveryProgress,
  hasRecoveryProgress,
  initEmptyRecoveryProgress,
} from './recoveryProgress';

const RECOVERY_RUN_KEY = 'recoveryRun';

export type RecoveryRunMarker = {
  started: true;
  startedAt: number;
  completed?: true;
  completedAt?: number;
};

function readJson<T>(key: string): T | null {
  if (typeof localStorage === 'undefined') return null;
  const raw = localStorage.getItem(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

export function getRecoveryRun(): RecoveryRunMarker | null {
  const marker = readJson<RecoveryRunMarker>(RECOVERY_RUN_KEY);
  return marker?.started === true ? marker : null;
}

/** Recovery started locally and not yet marked complete. */
export function isRecoveryInProgress(): boolean {
  const marker = getRecoveryRun();
  return marker != null && marker.completed !== true;
}

export function isRecoveryComplete(): boolean {
  const marker = getRecoveryRun();
  return marker?.completed === true;
}

/**
 * Mark recovery as started: run marker + empty progress ledger shell.
 * Call ensureRecoveryProgress() after backup write to populate entities.
 */
export function startRecoveryRun(): void {
  if (typeof localStorage === 'undefined') return;
  const marker: RecoveryRunMarker = {
    started: true,
    startedAt: Date.now(),
  };
  localStorage.setItem(RECOVERY_RUN_KEY, JSON.stringify(marker));
  initEmptyRecoveryProgress();
}

/**
 * Ensure recovery is started for resume. Keep existing progress if present.
 */
export function resumeRecoveryRun(): void {
  if (typeof localStorage === 'undefined') return;
  if (!isRecoveryInProgress()) {
    const marker: RecoveryRunMarker = {
      started: true,
      startedAt: Date.now(),
    };
    localStorage.setItem(RECOVERY_RUN_KEY, JSON.stringify(marker));
  }
  if (!hasRecoveryProgress()) {
    initEmptyRecoveryProgress();
  }
}

export function completeRecoveryRun(): void {
  if (typeof localStorage === 'undefined') return;
  const marker = getRecoveryRun();
  if (!marker) return;
  const completed: RecoveryRunMarker = {
    started: true,
    startedAt: marker.startedAt,
    completed: true,
    completedAt: Date.now(),
  };
  localStorage.setItem(RECOVERY_RUN_KEY, JSON.stringify(completed));
}

export function clearRecoveryRun(): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem(RECOVERY_RUN_KEY);
  clearRecoveryProgress();
}
