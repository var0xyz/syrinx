/**
 * Local import-run marker. Tracks backup restore before recovery handoff.
 */

const IMPORT_RUN_KEY = 'importRun';

export type ImportRunMarker = {
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

export function getImportRun(): ImportRunMarker | null {
  const marker = readJson<ImportRunMarker>(IMPORT_RUN_KEY);
  return marker?.started === true ? marker : null;
}

export function isImportInProgress(): boolean {
  const marker = getImportRun();
  return marker != null && marker.completed !== true;
}

export function isImportComplete(): boolean {
  const marker = getImportRun();
  return marker?.completed === true;
}

export function startImportRun(): void {
  if (typeof localStorage === 'undefined') return;
  const marker: ImportRunMarker = {
    started: true,
    startedAt: Date.now(),
  };
  localStorage.setItem(IMPORT_RUN_KEY, JSON.stringify(marker));
}

export function completeImportRun(): void {
  if (typeof localStorage === 'undefined') return;
  const marker = getImportRun();
  const completed: ImportRunMarker = {
    started: true,
    startedAt: marker?.startedAt ?? Date.now(),
    completed: true,
    completedAt: Date.now(),
  };
  localStorage.setItem(IMPORT_RUN_KEY, JSON.stringify(completed));
}

export function clearImportRun(): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem(IMPORT_RUN_KEY);
}
