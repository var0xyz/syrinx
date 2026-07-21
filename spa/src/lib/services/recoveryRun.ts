/**
 * Local recovery-run marker and minimal progress ledger (proposal 10/11).
 * Full entity enumeration and percent UI land in proposal 11.
 */

const RECOVERY_RUN_KEY = 'recoveryRun';
const RECOVERY_PROGRESS_KEY = 'recoveryProgress';

export type RecoveryRunMarker = {
  started: true;
  startedAt: number;
  completed?: true;
  completedAt?: number;
};

/** Minimal stub so resume vs scratch is detectable before proposal 11 fills it. */
export type RecoveryProgressLedger = {
  version: 1;
  entities: Record<string, unknown>;
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

export function hasRecoveryProgress(): boolean {
  const ledger = readJson<RecoveryProgressLedger>(RECOVERY_PROGRESS_KEY);
  return ledger != null && typeof ledger === 'object';
}

/**
 * Mark recovery as started: run marker + empty progress ledger.
 */
export function startRecoveryRun(): void {
  if (typeof localStorage === 'undefined') return;
  const marker: RecoveryRunMarker = {
    started: true,
    startedAt: Date.now(),
  };
  localStorage.setItem(RECOVERY_RUN_KEY, JSON.stringify(marker));
  const ledger: RecoveryProgressLedger = { version: 1, entities: {} };
  localStorage.setItem(RECOVERY_PROGRESS_KEY, JSON.stringify(ledger));
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
    const ledger: RecoveryProgressLedger = { version: 1, entities: {} };
    localStorage.setItem(RECOVERY_PROGRESS_KEY, JSON.stringify(ledger));
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
  localStorage.removeItem(RECOVERY_PROGRESS_KEY);
}
