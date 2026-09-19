import {
  pendingBackupsRepository,
  type BackupKind,
} from '$lib/repositories/pendingBackups';

/** Queue a backup telemetry event locally and try to send it now. */
export async function recordBackupEvent(kind: BackupKind): Promise<void> {
  const id = crypto.randomUUID();
  await pendingBackupsRepository.put({
    id,
    kind,
    timestamp: Date.now(),
  });
  await pendingBackupsRepository.syncPending();
}

/** Retry any backup telemetry events that failed to reach the server. */
export function syncPendingBackupEvents(): void {
  void pendingBackupsRepository.syncPending();
}
