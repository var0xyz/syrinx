import { writable } from 'svelte/store';
import { apiService } from '$lib/services/api';

const STORAGE_KEY = 'adminPermissions';

type Cached = { userID: string; permissions: string[] };

function load(): Cached | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (typeof parsed?.userID !== 'string' || !Array.isArray(parsed.permissions)) return null;
    return { userID: parsed.userID, permissions: parsed.permissions.filter((p: unknown) => typeof p === 'string') };
  } catch {
    return null;
  }
}

function save(state: Cached): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // best-effort persistence only
  }
}

/** The viewer's admin permissions, read once per app load and kept for the
 * session. A hint for what to show; the server checks every action. */
export const adminPermissions = writable<Cached | null>(load());

/** Reads userID's permissions in the background. */
export async function refreshAdminPermissions(userID: string): Promise<void> {
  try {
    const { permissions } = await apiService.getUserPermissions(userID);
    const next = { userID, permissions };
    save(next);
    adminPermissions.set(next);
  } catch (error) {
    console.error('Failed to refresh admin permissions:', error);
  }
}

type Viewer = { id?: string; role?: string } | null | undefined;

/** Whether the viewer holds permission. Root holds all. */
export function holds(viewer: Viewer, cached: Cached | null, permission: string): boolean {
  if (viewer?.role === 'root') return true;
  if (viewer?.role !== 'admin' || cached?.userID !== viewer.id) return false;
  return cached.permissions.includes(permission);
}

/** Whether the viewer may send broadcasts or private messages. */
export function canMail(viewer: Viewer, cached: Cached | null): boolean {
  return holds(viewer, cached, 'broadcast.message') || holds(viewer, cached, 'user.message');
}

/** The admin section's tabs, without Broadcast for a viewer who can't mail. */
export function adminTabs(showBroadcast: boolean): { href: string; label: string }[] {
  return [
    { href: '/admin/actions', label: 'Actions' },
    ...(showBroadcast ? [{ href: '/admin/broadcast', label: 'Broadcast' }] : []),
    { href: '/admin/mesh', label: 'Mesh' },
  ];
}
