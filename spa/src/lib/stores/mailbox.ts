import { derived, writable } from 'svelte/store';
import { mailboxRepository, type MailboxRecord } from '$lib/repositories/mailbox';

/** All locally-stored mailbox messages, newest first — the single source
 * of truth for both the bell badge and the popover contents. Refreshed
 * from IndexedDB on every WS receipt/mark-read/delete so the popover
 * never goes stale while the app stays open (messages arrive live over
 * WS — see specs/notifications/04 — there is no HTTP polling to trigger
 * a reload otherwise). */
export const mailboxMessages = writable<MailboxRecord[]>([]);

/** Whether any locally-stored mailbox message is unread — drives the
 * bell's red dot. Presence-based, not a count (see specs/notifications/05). */
export const mailboxUnread = derived(mailboxMessages, (messages) => messages.some((m) => !m.isRead));

export async function refreshMailboxMessages(): Promise<void> {
  mailboxMessages.set(await mailboxRepository.getAll());
}
