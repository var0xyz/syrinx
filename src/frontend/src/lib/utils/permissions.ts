/** Every admin permission, in display order. Mirrors adminPermissions in
 * admin.go. */
export const ADMIN_PERMISSIONS = [
  'admin.create',
  'admin.demote',
  'user.kick',
  'user.suspend',
  'user.rebind',
  'user.invites',
  'message.unicast',
  'message.broadcast',
  'mesh.invite',
  'mesh.connect',
  'mesh.disconnect',
] as const;

export type AdminPermission = (typeof ADMIN_PERMISSIONS)[number];

/** A permission set's names, or null when text isn't one: sorted, unique
 * and comma-joined, the one form the server accepts. */
export function parsePermissionSet(text: string): AdminPermission[] | null {
  if (!text) return [];
  const names = text.split(',');
  for (let i = 0; i < names.length; i++) {
    if (!(ADMIN_PERMISSIONS as readonly string[]).includes(names[i])) return null;
    if (i > 0 && names[i - 1] >= names[i]) return null;
  }
  return names as AdminPermission[];
}

/** perms in the one form parsePermissionSet accepts. */
export function formatPermissionSet(perms: Iterable<string>): string {
  return [...new Set(perms)].sort().join(',');
}
