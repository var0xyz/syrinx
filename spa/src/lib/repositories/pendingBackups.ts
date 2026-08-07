import { dbService } from '$lib/services/db';
import { apiService } from '$lib/services/api';
import { allowUnsigned } from '$lib/verifiers';

export type BackupKind = 'identity' | 'full';

export interface PendingBackupRecord {
  id: string;
  kind: BackupKind;
  timestamp: number;
}

export const pendingBackupsRepository = {
  async put(record: PendingBackupRecord): Promise<void> {
    await dbService.put('pendingBackups', record, allowUnsigned);
  },

  async delete(id: string): Promise<void> {
    await dbService.delete('pendingBackups', id);
  },

  async getAll(): Promise<PendingBackupRecord[]> {
    return dbService.getAll<PendingBackupRecord>('pendingBackups');
  },

  /** Flush queued backup telemetry to the server; leaves failed rows for retry. */
  async syncPending(): Promise<void> {
    const pending = await dbService.getAll<PendingBackupRecord>('pendingBackups');
    for (const record of pending) {
      try {
        await apiService.recordBackup(record.kind);
        await pendingBackupsRepository.delete(record.id);
      } catch (error) {
        console.warn('Failed to sync pending backup metric:', record.id, error);
      }
    }
  },
};
