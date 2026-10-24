import { writable } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildAdminDecisionPayload, buildAdminRequestPayload, buildFlagDismissalPayload } from './signing';
import { generateDismissalId } from '$lib/utils/id';
import { signedAtHeader } from './verify';
import type * as api from '$lib/types/api';
import type { AdminRecordMessage } from '$lib/proto/websocket_pb';
import { adminRequestsRepository } from '$lib/repositories/adminRequests';
import { flagsRepository } from '$lib/repositories/flags';

export type AdminAction =
  | 'suspend'
  | 'reinstate'
  | 'kick'
  | 'promote'
  | 'demote'
  | 'permissions'
  | 'rebind'
  | 'broadcast'
  | 'unicast';

/** The permission each action needs; a permissions change needs
 * admin.create to add and admin.demote to remove. */
export const ACTION_PERMISSION: Record<Exclude<AdminAction, 'permissions'>, string> = {
  suspend: 'user.suspend',
  reinstate: 'user.suspend',
  kick: 'user.kick',
  promote: 'admin.create',
  demote: 'admin.demote',
  rebind: 'user.rebind',
  broadcast: 'message.broadcast',
  unicast: 'message.unicast',
};

/** Whether r is a broadcast or unicast, listed under Message. */
export function isMailRequest(r: { action: string }): boolean {
  return r.action === 'broadcast' || r.action === 'unicast';
}

/** Bumped whenever a request is opened or decided here. */
export const adminRequestsChanged = writable(0);

/** Bumped whenever an admin record is stored or the admin stores cleared. */
export const adminRecordsChanged = writable(0);

function signer(): { userID: string; keyID: string } {
  const userID = localStorage.getItem('userId');
  const keyID = authService.getActiveKeyId();
  if (!userID || !keyID) throw new Error('Not signed in');
  return { userID, keyID };
}

/** Signs and opens a request a second admin must approve. */
export async function openAdminRequest(
  action: AdminAction,
  targets: string[],
  reason: string,
  body = ''
): Promise<api.AdminRequest> {
  const { userID, keyID } = signer();
  const id = crypto.randomUUID();
  const createdAt = Math.floor(Date.now() / 1000);
  const sorted = [...targets].sort();
  const armor = await requestSigner.sign(
    buildAdminRequestPayload(id, action, userID, keyID, reason, sorted, body, signedAtHeader(createdAt))
  );
  const req = await apiService.openAdminRequest({
    id,
    action,
    requesterId: userID,
    reason,
    targets: sorted,
    body,
    createdAt,
    requesterSignature: { id: keyID, armor },
  });
  await adminRequestsRepository.put(req).catch((err) => console.error('Failed to store an opened request:', err));
  adminRequestsChanged.update((n) => n + 1);
  return req;
}

/** Signs and sends an approval, rejection or withdrawal of req. */
export async function decideAdminRequest(
  req: api.AdminRequest,
  decision: 'approve' | 'reject' | 'withdraw'
): Promise<api.AdminRequest> {
  const { userID, keyID } = signer();
  const armor = await requestSigner.sign(
    buildAdminDecisionPayload(req.id, decision, userID, keyID, req.requesterSignature?.armor ?? '')
  );
  const closed = await apiService.decideAdminRequest(req.id, {
    decision,
    deciderId: userID,
    signature: { id: keyID, armor },
  });
  await adminRequestsRepository.put(closed).catch((err) => console.error('Failed to store a closed request:', err));
  adminRecordsChanged.update((n) => n + 1);
  adminRequestsChanged.update((n) => n + 1);
  return closed;
}

/** Stores a record pushed over the WebSocket. */
export async function storeAdminRecord(rec: AdminRecordMessage): Promise<void> {
  const r = rec.record;
  if (r.case === 'request') {
    await adminRequestsRepository.put(r.value);
  } else if (r.case === 'flag' || r.case === 'dismissal') {
    await (r.case === 'flag' ? flagsRepository.putFlag(r.value) : flagsRepository.putDismissal(r.value));
    await flagsRepository.settleCase(r.value.reedId);
  } else {
    return;
  }
  adminRecordsChanged.update((n) => n + 1);
}

/** Deletes every admin record this device holds. */
export async function clearAdminRecords(): Promise<void> {
  await adminRequestsRepository.clear();
  await flagsRepository.clearAdminCopies();
  adminRecordsChanged.update((n) => n + 1);
}

async function stored(put: () => Promise<boolean | void>, what: string): Promise<boolean> {
  try {
    return (await put()) !== false;
  } catch (err) {
    console.error('Failed to store', what, err);
    return false;
  }
}

/** Every page of a listing, in order. */
async function allPages<T>(page: (cursor: string) => Promise<{ items: T[]; next: string }>): Promise<T[]> {
  const out: T[] = [];
  let cursor = '';
  do {
    const p = await page(cursor);
    out.push(...p.items);
    cursor = p.next;
  } while (cursor);
  return out;
}

/** Pages back until a page holds nothing new. */
async function pagesUntilKnown<T>(
  page: (cursor: string) => Promise<{ items: T[]; next: string }>,
  store: (item: T) => Promise<boolean>
): Promise<void> {
  let cursor = '';
  do {
    const p = await page(cursor);
    let fresh = false;
    for (const item of p.items) if (await store(item)) fresh = true;
    cursor = fresh ? p.next : '';
  } while (cursor);
}

async function syncAdminRecordsNow(): Promise<void> {
  const { role } = await myPermissions();
  if (role !== 'admin' && role !== 'root') {
    await clearAdminRecords();
    return;
  }

  const requests = (state: 'pending' | 'closed') => (cursor: string) =>
    apiService.listAdminRequests(state, cursor).then((p) => ({ items: p.requests, next: p.nextCursor }));
  const open = await allPages(requests('pending'));
  for (const req of open) await stored(() => adminRequestsRepository.put(req), `request ${req.id}`);
  await pagesUntilKnown(
    requests('closed'),
    (req) => stored(() => adminRequestsRepository.put(req), `request ${req.id}`)
  );
  // A request held pending that's no longer open closed further back.
  const openIDs = new Set(open.map((r) => r.id));
  for (const held of await adminRequestsRepository.getAll()) {
    if (held.state !== 'pending' || openIDs.has(held.id)) continue;
    try {
      await adminRequestsRepository.put(await apiService.getAdminRequest(held.id));
    } catch (err) {
      console.error('Failed to fetch a closed request:', held.id, err);
    }
  }

  // A removed reporter's flags go when their account removal arrives.
  const touched = new Set<string>();
  const flagged = await allPages((c) => apiService.listFlags('open', c).then((p) => ({ items: p.reeds, next: p.nextCursor })));
  for (const fr of flagged) {
    touched.add(fr.reedId);
    for (const f of fr.flags) await stored(() => flagsRepository.putFlag(f), `flag on ${f.reedId}`);
  }
  await pagesUntilKnown(
    (c) => apiService.listFlags('dismissed', c).then((p) => ({ items: p.reeds, next: p.nextCursor })),
    async (fr) => {
      const d = fr.dismissal;
      if (!d || (await flagsRepository.hasDismissal(d.id))) return false;
      touched.add(fr.reedId);
      for (const f of fr.flags) await stored(() => flagsRepository.putFlag(f), `flag on ${f.reedId}`);
      return stored(() => flagsRepository.putDismissal(d), `dismissal ${d.id}`);
    }
  );
  await pagesUntilKnown(
    (c) => apiService.listFlags('withdrawn', c).then((p) => ({ items: p.reeds.flatMap((fr) => fr.flags), next: p.nextCursor })),
    async (f) => {
      if (await flagsRepository.isWithdrawn(f.reedId, f.reporterId)) return false;
      touched.add(f.reedId);
      return stored(() => flagsRepository.putFlag(f), `withdrawn flag on ${f.reedId}`);
    }
  );
  for (const reedId of touched) await flagsRepository.settleCase(reedId);
  adminRecordsChanged.update((n) => n + 1);
}

let syncing: Promise<void> | null = null;

/** Catches this device up on the admin records it may see, backfilling a
 * new admin, or deletes them once the viewer isn't an admin or root. */
export function syncAdminRecords(): Promise<void> {
  syncing ??= syncAdminRecordsNow()
    .catch((err) => console.error('Admin record sync failed:', err))
    .finally(() => (syncing = null));
  return syncing;
}

/** Signs and sends a dismissal of the open flags shown on reedID, and
 * keeps the countersigned dismissal. */
export async function dismissFlags(reedID: string, reason: string, flags: api.ReedFlag[]): Promise<api.FlagDismissal> {
  const { keyID } = signer();
  const id = generateDismissalId();
  const reporters = flags.map((f) => f.reporterId).sort();
  const armor = await requestSigner.sign(buildFlagDismissalPayload(id, reedID, reason, keyID, reporters));
  const dismissal = await apiService.dismissFlags(reedID, { id, reason, reporters, signature: { id: keyID, armor } });
  try {
    await flagsRepository.putDismissal(dismissal);
    await flagsRepository.settleCase(reedID);
  } catch (err) {
    console.error('Failed to store a dismissal:', err);
  }
  adminRecordsChanged.update((n) => n + 1);
  return dismissal;
}

/** The viewer's role and permissions. */
export async function myPermissions(): Promise<api.UserPermissions> {
  return apiService.getUserPermissions(signer().userID);
}

/** Why the viewer can't decide on req, null when the rules visible here
 * allow it. The server checks the rest (direct invitees, admin targets). */
export function decisionBlocker(
  req: api.AdminRequest,
  viewer: Pick<api.UserPermissions, 'role' | 'permissions'>,
  viewerID: string
): string | null {
  const isRoot = viewer.role === 'root';
  if (req.requesterId === viewerID && !isRoot) return 'You opened this request';
  if (req.targets.includes(viewerID)) return 'You’re a target of this request';
  if (req.action !== 'permissions') {
    const needed = ACTION_PERMISSION[req.action as Exclude<AdminAction, 'permissions'>];
    if (needed && !viewer.permissions.includes(needed)) return `You don’t hold ${needed}`;
  }
  return null;
}
