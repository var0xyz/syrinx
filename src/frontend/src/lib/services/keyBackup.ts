/**
 * The key backup is mandatory: <Auth> sends every signed-in page to /backup
 * until `lastKeyBackupAt` is set. A new active key isn't in any backup yet,
 * so making one active requires a fresh backup the same way.
 */

const LAST_BACKUP_KEY = 'lastKeyBackupAt';
const REASON_KEY = 'keyBackupReason';

export type KeyBackupReason = 'rotation';

/** Marks the active key as not backed up, so the next signed-in page is /backup. */
export function requireKeyBackup(reason: KeyBackupReason): void {
  try {
    localStorage.removeItem(LAST_BACKUP_KEY);
    localStorage.setItem(REASON_KEY, reason);
  } catch {
    // Without storage there's no gate to set; nothing else to do.
  }
}

/** Why a backup is being asked for, or null for the first one after signup. */
export function keyBackupReason(): KeyBackupReason | null {
  try {
    return localStorage.getItem(REASON_KEY) === 'rotation' ? 'rotation' : null;
  } catch {
    return null;
  }
}

/** Records a completed key backup, which lifts the /backup gate. */
export function markKeyBackedUp(timestamp: number): void {
  try {
    localStorage.setItem(LAST_BACKUP_KEY, String(timestamp));
    localStorage.removeItem(REASON_KEY);
  } catch {
    // Not remembered; the gate stays until storage works.
  }
}
