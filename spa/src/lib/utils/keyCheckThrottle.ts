const PREFIX = 'keyChecked:';
const WINDOW_MS = 60_000;

/** True if fingerprint has no recent re-check stamp, or the stamp is stale. */
export function shouldRecheck(fingerprint: string): boolean {
  const stamped = sessionStorage.getItem(PREFIX + fingerprint);
  if (!stamped) return true;
  const at = Number(stamped);
  if (Number.isNaN(at)) return true;
  return Date.now() - at >= WINDOW_MS;
}

/** Stamps fingerprint as checked now. Tab-scoped; no cleanup needed. */
export function markChecked(fingerprint: string): void {
  sessionStorage.setItem(PREFIX + fingerprint, String(Date.now()));
}
