import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

/** Which popover tab a message belongs to — mirrors db.go's
 * MailboxCategory. Server-decided at send time, not inferred from kind. */
export type MailboxCategory = 'system' | 'interaction';

/** Locally-decrypted mailbox message. isRead/local existence are purely
 * client-side state — the server has already deleted its copy by the time
 * this is stored (see specs/notifications/00, 05). */
export interface MailboxRecord {
  id: string;
  kind: string;
  category: MailboxCategory;
  message: string;
  link?: string;
  senderUserID: string;
  meta?: unknown;
  isRead: boolean;
  createdAt: string;
}

export const mailboxRepository = {
  async put(record: MailboxRecord): Promise<void> {
    await dbService.put('mailbox', record, allowUnsigned);
  },

  async get(id: string): Promise<MailboxRecord | null> {
    return dbService.get<MailboxRecord>('mailbox', id);
  },

  /** Newest first. */
  async getAll(): Promise<MailboxRecord[]> {
    return dbService.getLatestFromIndex<MailboxRecord>('mailbox', 'createdAt', Number.MAX_SAFE_INTEGER);
  },

  async hasUnread(): Promise<boolean> {
    const all = await mailboxRepository.getAll();
    return all.some((m) => !m.isRead);
  },

  async markRead(id: string): Promise<void> {
    const record = await mailboxRepository.get(id);
    if (!record || record.isRead) return;
    await mailboxRepository.put({ ...record, isRead: true });
  },

  async markAllRead(): Promise<void> {
    const all = await mailboxRepository.getAll();
    await Promise.all(
      all.filter((m) => !m.isRead).map((m) => mailboxRepository.put({ ...m, isRead: true }))
    );
  },

  async delete(id: string): Promise<void> {
    await dbService.delete('mailbox', id);
  },
};
