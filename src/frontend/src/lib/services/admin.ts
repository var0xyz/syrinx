import { writable } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { requestSigner } from './request-signer';
import { buildAdminDecisionPayload, buildAdminRequestPayload } from './signing';
import { signedAtHeader } from './verify';
import type * as api from '$lib/types/api';
import { adminRequestsRepository } from '$lib/repositories/adminRequests';

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
  adminRequestsChanged.update((n) => n + 1);
  return closed;
}

const PENDING_SEEN_KEY = 'adminPendingSeen';

function loadPendingSeen(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(PENDING_SEEN_KEY) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter((id) => typeof id === 'string') : [];
  } catch {
    return [];
  }
}

function savePendingSeen(ids: string[]): void {
  try {
    localStorage.setItem(PENDING_SEEN_KEY, JSON.stringify(ids));
  } catch {
    // best-effort persistence only
  }
}

async function storeClosed(req: api.AdminRequest): Promise<boolean> {
  if (req.state === 'pending' || (await adminRequestsRepository.has(req.id))) return false;
  try {
    await adminRequestsRepository.put(req);
    return true;
  } catch (err) {
    console.error('Failed to store a closed request:', req.id, err);
    return false;
  }
}

/** Stores the closed requests this device lacks, given the open ones the
 * server just listed. Pages back until a page holds nothing new, then
 * fetches any request seen open last time that has since closed further
 * back. Returns whether anything was stored. */
export async function syncClosedAdminRequests(pendingIDs: string[]): Promise<boolean> {
  let stored = false;
  let cursor = '';
  do {
    const page = await apiService.listAdminRequests('closed', cursor);
    let fresh = false;
    for (const req of page.requests) {
      if (await storeClosed(req)) fresh = stored = true;
    }
    cursor = fresh ? page.nextCursor : '';
  } while (cursor);

  const open = new Set(pendingIDs);
  const retry: string[] = [];
  for (const id of loadPendingSeen()) {
    if (open.has(id) || (await adminRequestsRepository.has(id))) continue;
    try {
      if (await storeClosed(await apiService.getAdminRequest(id))) stored = true;
    } catch (err) {
      console.error('Failed to fetch a closed request:', id, err);
      retry.push(id);
    }
  }
  savePendingSeen([...pendingIDs, ...retry]);
  return stored;
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
