/**
 * Origin-local device id for server-side single-active-device binding.
 * Never exported in backups.
 */

const STORAGE_KEY = 'deviceId';

export function ensureDeviceId(): string {
  if (typeof localStorage === 'undefined') {
    return '';
  }
  let id = localStorage.getItem(STORAGE_KEY);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(STORAGE_KEY, id);
  }
  return id;
}

export function getDeviceId(): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem(STORAGE_KEY);
}

export function deviceIdHeader(): Record<string, string> {
  const id = ensureDeviceId();
  return id ? { 'X-Syrinx-Device-Id': id } : {};
}
